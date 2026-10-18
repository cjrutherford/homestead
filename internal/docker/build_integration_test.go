package docker

import (
	"archive/tar"
	"archive/zip"
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
	// Both artifact formats the shared Dockerfile extracts (S249).
	t.Run("tar.gz", func(t *testing.T) { realCraftBuild(t, "9.9.9", "tar.gz", writeTarGz) })
	t.Run("zip", func(t *testing.T) { realCraftBuild(t, "9.9.8", "zip", writeZip) })
}

func realCraftBuild(t *testing.T, version, ext string, write func(*testing.T, string, map[string]string)) {
	repo, _ := filepath.Abs("../..")
	dir := t.TempDir()
	// Synthetic site: HTML entry, a hashed wasm, an unhashed js, and a
	// dotfile the build must drop.
	artifact := "wordcraft-" + version + "." + ext
	site := filepath.Join(dir, artifact)
	write(t, site, map[string]string{
		"index.html":            "<html>hello</html>",
		"app.0123456789ab.wasm": "\x00asm",
		"main.js":               "console.log(1)",
		".secret":               "do not serve",
	})
	data, _ := os.ReadFile(site)
	sum := sha256.Sum256(data)
	ctxDigest, err := craft.TreeDigest(filepath.Join(repo, "images", "craft"))
	if err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf("format: %s\ncounter: 1\ncontext_digest: %s\napps:\n  - name: wordcraft\n    version: %s\n    artifact:\n      name: %s\n      sha256: %s\n      size: %d\n",
		craft.ManifestFormat, ctxDigest, version, artifact, hex.EncodeToString(sum[:]), len(data))
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
	tag := craft.Tag("wordcraft", version)
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
	// Dotfiles from the artifact are never served.
	if r := get("/.secret"); r.StatusCode != 404 {
		t.Fatalf("/.secret = %d, want 404", r.StatusCode)
	}
	wasmReq, _ := http.NewRequest(http.MethodGet, base+"/app.0123456789ab.wasm", nil)
	wasmReq.Header.Set("Accept-Encoding", "gzip")
	wasmResp, err := (&http.Transport{DisableCompression: true}).RoundTrip(wasmReq)
	if err != nil || wasmResp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("wasm not gzip-encoded: %v %v", err, wasmResp)
	}
	// Behind the Caddy edge every request carries Via; nginx must still
	// compress (gzip_proxied any), or the wasm ships at full size.
	viaReq, _ := http.NewRequest(http.MethodGet, base+"/app.0123456789ab.wasm", nil)
	viaReq.Header.Set("Accept-Encoding", "gzip")
	viaReq.Header.Set("Via", "1.1 Caddy")
	viaResp, err := (&http.Transport{DisableCompression: true}).RoundTrip(viaReq)
	if err != nil || viaResp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("wasm not gzip-encoded behind a proxy (Via): %v %v", err, viaResp)
	}
	// The Homestead wrapper page (S254) is served from the image with its
	// own headers, and nginx never serves the file path itself.
	page := get("/_homestead/open.html")
	body, _ := io.ReadAll(page.Body)
	page.Body.Close()
	if page.StatusCode != 200 || !strings.Contains(page.Header.Get("Content-Type"), "text/html") || !strings.Contains(string(body), "/_homestead/open.js") {
		t.Fatalf("wrapper page = %d %v", page.StatusCode, page.Header)
	}
	csp := page.Header.Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "script-src 'self'", "connect-src 'self'", "frame-src 'self'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Fatalf("wrapper CSP lacks %q: %q", want, csp)
		}
	}
	if strings.Contains(string(body), "<script>") || strings.Contains(csp, "unsafe-") {
		t.Fatalf("wrapper must not need inline script or unsafe CSP: %q", csp)
	}
	if page.Header.Get("Cache-Control") != "no-cache" || page.Header.Get("X-Content-Type-Options") != "nosniff" || page.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("wrapper headers: %v", page.Header)
	}
	for path, wantType := range map[string]string{"/_homestead/open.js": "javascript", "/_homestead/open.css": "text/css"} {
		r := get(path)
		_, _ = io.Copy(io.Discard, r.Body)
		r.Body.Close()
		if r.StatusCode != 200 || !strings.Contains(r.Header.Get("Content-Type"), wantType) || r.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("%s = %d %v", path, r.StatusCode, r.Header)
		}
	}
	for _, path := range []string{"/_homestead/files/session", "/_homestead/files/f/a.xlsx", "/_homestead/nope"} {
		if r := get(path); r.StatusCode != 404 {
			t.Fatalf("%s = %d, want 404 from nginx", path, r.StatusCode)
		}
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

func writeZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}
