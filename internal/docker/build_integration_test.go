package docker

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/craft"
	"homestead/internal/release"
	"homestead/internal/store"
)

// TestRealCraftBuild runs the whole verify-then-build pipeline against the
// real Docker Engine with a synthetic artifact and a throwaway key, then
// serves the image and checks the static-server behavior. It needs Docker
// and network access to pull the digest-pinned base, so it only runs when
// HOMESTEAD_DOCKER_TESTS=1.
func TestRealCraftBuild(t *testing.T) {
	if os.Getenv("HOMESTEAD_DOCKER_TESTS") != "1" {
		t.Skip("set HOMESTEAD_DOCKER_TESTS=1 to build a real image")
	}
	repo, _ := filepath.Abs("../..")
	dir := t.TempDir()
	// Synthetic site: HTML entry, a hashed wasm, an unhashed js.
	site := filepath.Join(dir, "wordcraft-9.9.9.tar.gz")
	writeTarGz(t, site, map[string]string{
		"index.html":            "<html>hello</html>",
		"app.0123456789ab.wasm": "\x00asm",
		"main.js":               "console.log(1)",
	})
	data, _ := os.ReadFile(site)
	sum := sha256.Sum256(data)
	ctxDigest, err := craft.TreeDigest(filepath.Join(repo, "images", "craft"))
	if err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf("format: %s\ncounter: 1\ncontext_digest: %s\napps:\n  - name: wordcraft\n    version: 9.9.9\n    artifact:\n      name: wordcraft-9.9.9.tar.gz\n      sha256: %s\n      size: %d\n",
		craft.ManifestFormat, ctxDigest, hex.EncodeToString(sum[:]), len(data))
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	var id [8]byte
	_, _ = rand.Read(id[:])
	sig := ed25519.Sign(priv, []byte(manifest))
	global := ed25519.Sign(priv, append(append([]byte(nil), sig...), []byte("t")...))
	sigText := "untrusted comment: s\n" + base64.StdEncoding.EncodeToString(append(append([]byte("Ed"), id[:]...), sig...)) +
		"\ntrusted comment: t\n" + base64.StdEncoding.EncodeToString(global) + "\n"
	pubKey, err := release.ParsePublicKey(base64.StdEncoding.EncodeToString(append(append([]byte("Ed"), id[:]...), pub...)))
	if err != nil {
		t.Fatal(err)
	}
	mPath := filepath.Join(dir, "m.yaml")
	_ = os.WriteFile(mPath, []byte(manifest), 0o644)
	_ = os.WriteFile(mPath+".minisig", []byte(sigText), 0o644)

	st, err := store.Open(filepath.Join(dir, "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p := &craft.Pipeline{
		Keys:       func() ([]release.PublicKey, error) { return []release.PublicKey{pubKey}, nil },
		Store:      craft.NewStore(st),
		Builder:    BuildClient{},
		ContextDir: filepath.Join(repo, "images", "craft"),
		StagingDir: filepath.Join(dir, "staging"),
	}
	req := craft.Request{App: "wordcraft", ManifestPath: mPath, SignaturePath: mPath + ".minisig", ArtifactPath: site, Actor: "test"}
	def, err := p.Definition(req)
	if err != nil {
		t.Fatal(err)
	}
	defer def.Cleanup()
	for _, step := range def.Steps {
		if err := step.Run(context.Background()); err != nil {
			t.Fatalf("step %s: %v", step.Name, err)
		}
	}
	tag := craft.Tag("wordcraft", "9.9.9")
	t.Cleanup(func() { _ = exec.Command("docker", "rmi", tag).Run() })
	if err := p.CheckImage(context.Background(), tag); err != nil {
		t.Fatal(err)
	}
	// Run it read-only with only /tmp writable, as the craft packages do (S249).
	name := "craft-it-" + hex.EncodeToString(id[:4])
	if out, err := exec.Command("docker", "run", "-d", "--name", name, "--read-only", "--tmpfs", "/tmp", "--cap-drop", "ALL", "-p", "127.0.0.1::8080", tag).CombinedOutput(); err != nil {
		t.Fatalf("run: %v %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })
	port, err := exec.Command("docker", "port", name, "8080/tcp").Output()
	if err != nil {
		logs, _ := exec.Command("docker", "logs", name).CombinedOutput()
		t.Fatalf("container not serving: %v\n%s", err, logs)
	}
	base := "http://" + strings.TrimSpace(strings.Split(string(port), "\n")[0])
	get := func(path string) *http.Response {
		var resp *http.Response
		for i := 0; i < 50; i++ {
			resp, err = http.Get(base + path)
			if err == nil {
				return resp
			}
			exec.Command("sleep", "0.2").Run()
		}
		t.Fatalf("GET %s: %v", path, err)
		return nil
	}
	if r := get("/"); r.StatusCode != 200 || r.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("/ = %d cache %q", r.StatusCode, r.Header.Get("Cache-Control"))
	}
	r := get("/app.0123456789ab.wasm")
	if r.Header.Get("Content-Type") != "application/wasm" || !strings.Contains(r.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("wasm headers: %v", r.Header)
	}
	_, _ = io.Copy(io.Discard, r.Body)
	if r := get("/main.js"); r.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("main.js cache %q", r.Header.Get("Cache-Control"))
	}
	// The shared shape (S249): a healthz, a non-root server, nosniff, and
	// the wasm served gzip-encoded to a client that accepts it.
	if r := get("/healthz"); r.StatusCode != 200 {
		t.Fatalf("/healthz = %d", r.StatusCode)
	}
	if out, err := exec.Command("docker", "exec", name, "id", "-un").Output(); err != nil || strings.TrimSpace(string(out)) != "nginx" {
		t.Fatalf("server user = %q (%v)", out, err)
	}
	if r := get("/"); r.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("nosniff missing: %v", r.Header)
	}
	wasmReq, _ := http.NewRequest(http.MethodGet, base+"/app.0123456789ab.wasm", nil)
	wasmReq.Header.Set("Accept-Encoding", "gzip")
	wasmResp, err := (&http.Transport{DisableCompression: true}).RoundTrip(wasmReq)
	if err != nil || wasmResp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("wasm not gzip-encoded: %v %v", err, wasmResp)
	}
}

func writeTarGz(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))})
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	_ = gz.Close()
}
