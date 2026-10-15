package craft

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// manifestApp writes a signed manifest for an arbitrary app/artifact.
func (e *env) manifestApp(app, version string, counter int64, artifact []byte) Request {
	e.t.Helper()
	digest, _ := TreeDigest(e.ctxDir)
	sum := sha256.Sum256(artifact)
	text := fmt.Sprintf("format: %s\ncounter: %d\ncontext_digest: %s\napps:\n  - name: %s\n    version: %s\n    artifact:\n      name: %s-%s.tar.gz\n      urls:\n        - https://example.invalid/a.tar.gz\n      sha256: %s\n      size: %d\n",
		ManifestFormat, counter, digest, app, version, app, version, hex.EncodeToString(sum[:]), len(artifact))
	req := e.write(text, e.signer.sign([]byte(text), "Ed"))
	req.App = app
	return req
}

func TestCounterIsTrainWide(t *testing.T) {
	e := newEnv(t)
	// gridcraft built from counter 9; a replayed counter-5 wordcraft
	// manifest (an app never built) is refused.
	if err := e.build(e.manifestApp("gridcraft", "1.0.0", 9, e.artifact)); err != nil {
		t.Fatal(err)
	}
	err := e.build(e.manifestApp("wordcraft", "1.0.0", 5, e.artifact))
	wantCheck(t, err, CheckCounter)
	var ce *CheckError
	if !errors.As(err, &ce) || !ce.Overridable() {
		t.Fatal("train-wide refusal stays overridable")
	}
	// Equal or higher counters pass.
	if err := e.build(e.manifestApp("wordcraft", "1.0.0", 9, e.artifact)); err != nil {
		t.Fatalf("counter 9: %v", err)
	}
	// The audited override still works.
	req := e.manifestApp("pdfcraft", "1.0.0", 5, e.artifact)
	req.AllowOlder = true
	if err := e.build(req); err != nil {
		t.Fatalf("override: %v", err)
	}
}

func TestEqualCounterSameVersionDifferentDigestsRefused(t *testing.T) {
	e := newEnv(t)
	if err := e.build(e.manifestFor("1.0.0", 5, nil)); err != nil {
		t.Fatal(err)
	}
	other := tarGz(t, map[string]string{"index.html": "different"})
	req := e.manifestApp("wordcraft", "1.0.0", 5, other)
	req.AllowOlder = true
	e.fetchBody = other
	err := e.build(req)
	wantCheck(t, err, CheckCounterConflict)
	e.fetchBody = e.artifact
	// The identical manifest again is a plain rebuild.
	if err := e.build(e.manifestFor("1.0.0", 5, nil)); err != nil {
		t.Fatalf("identical rebuild: %v", err)
	}
}

func TestConcurrentBuildsCannotLowerBaseline(t *testing.T) {
	e := newEnv(t)
	reqs := []Request{e.manifestApp("wordcraft", "1.0.0", 5, e.artifact), e.manifestApp("wordcraft", "1.1.0", 9, e.artifact)}
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = e.build(reqs[i%2])
		}(i)
	}
	wg.Wait()
	base, err := e.pipe.Store.Baseline("wordcraft")
	if err != nil || base.Counter != 9 || base.Version != "1.1.0" {
		t.Fatalf("baseline = %+v err = %v", base, err)
	}
}

func TestGuardedUpsertRefusesLowering(t *testing.T) {
	e := newEnv(t)
	hi := Image{App: "wordcraft", Version: "1.1.0", Tag: "t", ImageID: "i", Counter: 9}
	if err := e.pipe.Store.putImage(hi, false); err != nil {
		t.Fatal(err)
	}
	lo := Image{App: "wordcraft", Version: "1.0.0", Tag: "t0", ImageID: "i0", Counter: 5}
	if err := e.pipe.Store.putImage(lo, false); !errors.Is(err, ErrBaselineLowered) {
		t.Fatalf("err = %v", err)
	}
	if err := e.pipe.Store.putImage(lo, true); err != nil {
		t.Fatalf("override: %v", err)
	}
}

func TestMirrorFallbackOnMismatch(t *testing.T) {
	e := newEnv(t)
	req := e.manifestFor("1.0.0", 1, func(s *string) {})
	// Two mirrors: the first serves wrong bytes, the second the right ones.
	data, _ := os.ReadFile(req.ManifestPath)
	text := strings.Replace(string(data), "        - https://example.invalid/wordcraft-1.0.0.tar.gz\n",
		"        - https://bad.invalid/a\n        - https://good.invalid/a\n", 1)
	req = e.write(text, e.signer.sign([]byte(text), "Ed"))
	e.pipe.Fetch = func(_ context.Context, raw string, dst io.Writer, limit int64) error {
		if strings.Contains(raw, "bad.") {
			_, err := dst.Write([]byte("evil"))
			return err
		}
		_, err := dst.Write(e.artifact)
		return err
	}
	if err := e.build(req); err != nil {
		t.Fatalf("fallback: %v", err)
	}
	// All mirrors bad: every failure is reported.
	e.pipe.Fetch = func(_ context.Context, raw string, dst io.Writer, limit int64) error {
		_, err := dst.Write([]byte("evil"))
		return err
	}
	err := e.build(req)
	wantCheck(t, err, CheckArtifact)
	if !strings.Contains(err.Error(), "bad.invalid") || !strings.Contains(err.Error(), "good.invalid") {
		t.Fatalf("not all failures reported: %v", err)
	}
}

func TestFetchLimitIsSizePlusOne(t *testing.T) {
	e := newEnv(t)
	var gotLimit int64
	e.pipe.Fetch = func(_ context.Context, _ string, dst io.Writer, limit int64) error {
		gotLimit = limit
		_, err := dst.Write(append(append([]byte(nil), e.artifact...), 'x')) // size+1 bytes
		return err
	}
	wantCheck(t, e.build(e.manifestFor("1.0.0", 1, nil)), CheckArtifact)
	if gotLimit != int64(len(e.artifact))+1 {
		t.Fatalf("limit = %d, want size+1 = %d", gotLimit, len(e.artifact)+1)
	}
	e.assertNothingBuilt()
}

func TestArchiveSafety(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	tarWith := func(h *tar.Header, body string) []byte {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		h.Size = int64(len(body))
		if h.Mode == 0 {
			h.Mode = 0o644
		}
		_ = tw.WriteHeader(h)
		_, _ = tw.Write([]byte(body))
		_ = tw.Close()
		_ = gz.Close()
		return buf.Bytes()
	}
	bad := map[string][]byte{
		"slip.tar.gz":    tarWith(&tar.Header{Name: "../evil", Typeflag: tar.TypeReg}, "x"),
		"abs.tar.gz":     tarWith(&tar.Header{Name: "/etc/evil", Typeflag: tar.TypeReg}, "x"),
		"nested.tar.gz":  tarWith(&tar.Header{Name: "a/../../evil", Typeflag: tar.TypeReg}, "x"),
		"symlink.tar.gz": tarWith(&tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}, ""),
		"hard.tar.gz":    tarWith(&tar.Header{Name: "hard", Typeflag: tar.TypeLink, Linkname: "index.html"}, ""),
		"dev.tar.gz":     tarWith(&tar.Header{Name: "dev", Typeflag: tar.TypeChar}, ""),
		"bomb.tar.gz":    tarWith(&tar.Header{Name: "big", Typeflag: tar.TypeReg}, strings.Repeat("0", 4096)),
	}
	for name, data := range bad {
		if err := scanArchive(write(name, data), name, 1024); !errors.Is(err, ErrArchive) {
			t.Errorf("%s: err = %v, want ErrArchive", name, err)
		}
	}
	good := tarGz(t, map[string]string{"index.html": "ok", "sub/a.js": "1"})
	if err := scanArchive(write("good.tar.gz", good), "good.tar.gz", 1024); err != nil {
		t.Fatalf("good archive: %v", err)
	}
	// Zip: slip, symlink and bomb.
	zipWith := func(name string, mode os.FileMode, body string) []byte {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		h.SetMode(mode)
		w, _ := zw.CreateHeader(h)
		_, _ = w.Write([]byte(body))
		_ = zw.Close()
		return buf.Bytes()
	}
	for name, data := range map[string][]byte{
		"slip.zip":    zipWith("../evil", 0o644, "x"),
		"symlink.zip": zipWith("link", os.ModeSymlink|0o777, "/etc/passwd"),
		"bomb.zip":    zipWith("big", 0o644, strings.Repeat("0", 4096)),
	} {
		if err := scanArchive(write(name, data), name, 1024); !errors.Is(err, ErrArchive) {
			t.Errorf("%s: err = %v, want ErrArchive", name, err)
		}
	}
	if err := scanArchive(write("ok.zip", zipWith("index.html", 0o644, "ok")), "ok.zip", 1024); err != nil {
		t.Fatalf("good zip: %v", err)
	}
}

func TestBuildRefusesUnsafeArchiveBeforeBuilding(t *testing.T) {
	e := newEnv(t)
	e.artifact = tarGz(t, map[string]string{"../evil": "x"})
	e.fetchBody = e.artifact
	wantCheck(t, e.build(e.manifestFor("1.0.0", 1, nil)), CheckArtifact)
	e.assertNothingBuilt()
	// Bomb: expansion cap below the content.
	e.artifact = tarGz(t, map[string]string{"index.html": strings.Repeat("a", 5000)})
	e.fetchBody = e.artifact
	e.pipe.MaxExpanded = 1000
	wantCheck(t, e.build(e.manifestFor("1.0.0", 1, nil)), CheckArtifact)
	e.assertNothingBuilt()
	// The disk preflight is sized from the cap.
	if got := e.pipe.required(10); got != 20+2*1000+DiskHeadroom {
		t.Fatalf("required = %d", got)
	}
}

func TestDockerfileMustBePinned(t *testing.T) {
	dir := t.TempDir()
	check := func(body string) error {
		p := filepath.Join(dir, "Dockerfile")
		_ = os.WriteFile(p, []byte(body), 0o644)
		return checkDockerfilePinned(p)
	}
	pin := "nginx:1.27@sha256:" + strings.Repeat("a", 64)
	if err := check("ARG BASE=" + pin + "\nFROM ${BASE} AS x\nFROM ${BASE}\nCOPY --from=x /a /a\n"); err != nil {
		t.Fatalf("pinned via ARG: %v", err)
	}
	if err := check("FROM " + pin + " AS a\nFROM a\nFROM scratch\n"); err != nil {
		t.Fatalf("stages and scratch: %v", err)
	}
	for _, bad := range []string{"FROM nginx:1.27\n", "ARG BASE=nginx\nFROM ${BASE}\n", "FROM " + pin + "\nFROM alpine\n", "FROM nginx@sha256:abc\n"} {
		if err := check(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	// The shipped Dockerfile is pinned.
	if err := checkDockerfilePinned("../../images/craft/Dockerfile"); err != nil {
		t.Fatalf("images/craft/Dockerfile: %v", err)
	}
}

func TestBuildRefusesUnpinnedDockerfile(t *testing.T) {
	e := newEnv(t)
	if err := os.WriteFile(filepath.Join(e.ctxDir, "Dockerfile"), []byte("FROM nginx:latest\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Signed digest covers the unpinned file, so only the pin check refuses.
	wantCheck(t, e.build(e.manifestFor("1.0.0", 1, nil)), CheckContext)
	e.assertNothingBuilt()
}

func TestStagingDirHardening(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	_ = os.Mkdir(real, 0o755)
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := ensureStaging(link); err == nil {
		t.Fatal("symlinked staging accepted")
	}
	if err := ensureStaging(real); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(real); info.Mode().Perm() != 0o700 {
		t.Fatalf("mode = %v, want 0700", info.Mode().Perm())
	}
	fresh := filepath.Join(base, "new")
	if err := ensureStaging(fresh); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(fresh); info.Mode().Perm() != 0o700 {
		t.Fatalf("new mode = %v", info.Mode().Perm())
	}
}

func TestHTTPFetchPolicy(t *testing.T) {
	body := []byte("published bytes")
	mux := http.NewServeMux()
	mux.HandleFunc("/plain", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept-Encoding") != "identity" {
			t.Errorf("Accept-Encoding = %q, want identity", r.Header.Get("Accept-Encoding"))
		}
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/gzip", func(w http.ResponseWriter, r *http.Request) {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		_, _ = gz.Write(body)
		_ = gz.Close()
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(buf.Bytes())
	})
	mux.HandleFunc("/to-http", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.invalid/x", http.StatusFound)
	})
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	var out bytes.Buffer
	if err := fetchWith(context.Background(), client, srv.URL+"/plain", &out, 1<<20); err != nil || !bytes.Equal(out.Bytes(), body) {
		t.Fatalf("plain: %v %q", err, out.String())
	}
	out.Reset()
	if err := fetchWith(context.Background(), client, srv.URL+"/gzip", &out, 1<<20); err == nil {
		t.Fatal("gzip Content-Encoding accepted")
	}
	if err := fetchWith(context.Background(), client, srv.URL+"/to-http", &out, 1<<20); err == nil {
		t.Fatal("redirect to http accepted")
	}
	if err := fetchWith(context.Background(), client, "http://example.invalid/x", &out, 10); err == nil {
		t.Fatal("http URL accepted")
	}
}
