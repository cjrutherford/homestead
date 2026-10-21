package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/appsso"
	"homestead/internal/docker"
	"homestead/internal/operation"
	"homestead/internal/store"
)

// uninstallGuardServer is the uninstall fixture plus a handle on its store.
func uninstallGuardServer(t *testing.T) (*Server, *store.Store, string) {
	t.Helper()
	srv, catalogDir := catalogTestServer(t)
	st, err := store.Open(filepath.Join(filepath.Dir(catalogDir), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	demo, err := os.ReadFile(filepath.Join(catalogDir, "demo.yaml"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	authentik := strings.NewReplacer("demo", "authentik", "Demo", "Authentik", "name: web", "name: server").Replace(string(demo))
	if err := os.WriteFile(filepath.Join(catalogDir, "authentik.yaml"), []byte(authentik), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	for _, name := range []string{"demo", authentikPackage} {
		stack := stackDirFor(name)
		if err := os.MkdirAll(stack, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(stack, "compose.yaml"), []byte("name: "+name+"\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	return srv, st, catalogDir
}

func TestUninstallRefusesPublicNames(t *testing.T) {
	srv, st, _ := uninstallGuardServer(t)
	route := docker.Route{Host: "demo.lan", Target: docker.Target{Host: "web", Port: 8080}, Exposure: docker.ExposureLocal,
		Tunnel: []docker.Hostname{"demo.example.com"}}
	if err := saveEdgeRoutes(st, docker.RouteSet{Routes: []docker.Route{route}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/demo/uninstall", map[string]any{"leave_sign_in": true})
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "demo.example.com") ||
		!strings.Contains(recorder.Body.String(), "apps public plan demo --off") || recorder.Header().Get("X-Homestead-Refusal") != refusePublicNames {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	if _, err := os.Stat(filepath.Join(stackDirFor("demo"), "compose.yaml")); err != nil {
		t.Fatalf("refusal removed the stack: %v", err)
	}
	// Without the public name the uninstall goes ahead.
	route.Tunnel = nil
	if err := saveEdgeRoutes(st, docker.RouteSet{Routes: []docker.Route{route}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/demo/uninstall", map[string]any{})
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
}

func TestUninstallSignInNeedsTurningOffOrLeaving(t *testing.T) {
	srv, st, _ := uninstallGuardServer(t)
	records := appsso.Records{DB: st.DB()}
	if err := records.Save(appsso.Record{Package: "demo", Adapter: "immich", ClientID: "homestead-demo"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/demo/uninstall", map[string]any{})
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "apps sign-in plan demo --off") ||
		!strings.Contains(recorder.Body.String(), "leave_sign_in") || recorder.Header().Get("X-Homestead-Refusal") != refuseSignIn {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	// Authentik cannot go while an app signs in through it, acknowledged or not.
	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/authentik/uninstall", map[string]any{"leave_sign_in": true})
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "(demo)") || recorder.Header().Get("X-Homestead-Refusal") != refuseSignInDependents {
		t.Fatalf("authentik: code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	// Leaving it in Authentik uninstalls, then forgets the record.
	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/catalog/demo/uninstall", map[string]any{"leave_sign_in": true})
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	op := waitOperation(t, st, recorder.Body.Bytes())
	if op.Status != operation.StatusSucceeded {
		t.Fatalf("operation: %+v", op)
	}
	if _, on, err := records.Get("demo"); err != nil || on {
		t.Fatalf("record kept: %v %v", on, err)
	}
	var detail string
	if err := st.DB().QueryRow(`SELECT detail FROM audit_entries WHERE action = 'app.uninstall.leave-sign-in'`).Scan(&detail); err != nil ||
		!strings.Contains(detail, "homestead-demo") {
		t.Fatalf("audit: %q %v", detail, err)
	}
}
