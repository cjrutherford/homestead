package server

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homestead/internal/docker"
)

// dockgeFake serves the Engine API subsets Health needs: ping,
// version, info, containers, images, and networks. The project label
// on the container toggles installed.
func dockgeFake(t *testing.T, project string, running bool) *docker.Client {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "docker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	state := "exited"
	if running {
		state = "running"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/_ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Version":"29.1.2","ApiVersion":"1.52"}`))
	})
	mux.HandleFunc("/info", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Driver":"overlayfs"}`))
	})
	mux.HandleFunc("/containers/json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"Id":"abc","Names":["/homestead-dockge"],"Image":"louislam/dockge:1.5.0","State":"` + state + `","Status":"Up","Labels":{"com.docker.compose.project":"` + project + `"}}]`))
	})
	mux.HandleFunc("/images/json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	})
	mux.HandleFunc("/networks", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	})
	server := &http.Server{Handler: mux}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, err := net.DialTimeout("unix", socket, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("docker fake never accepted: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return docker.NewClient(socket)
}

func dockgeTestServer(t *testing.T, project string, running bool) *Server {
	t.Helper()
	srv := New("127.0.0.1:0")
	srv.MountDockge(dockgeFake(t, project, running))
	return srv
}

func TestDockgeLink(t *testing.T) {
	srv := dockgeTestServer(t, "dockge", true)
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/dockge", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	var link struct {
		Host      string   `json:"host"`
		URL       string   `json:"url"`
		Exposure  string   `json:"exposure"`
		Aligned   bool     `json:"aligned"`
		Findings  []string `json:"findings"`
		Installed bool     `json:"installed"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &link); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if link.Host != "dockge.lan" || link.URL != "https://dockge.lan" {
		t.Fatalf("link = %+v", link)
	}
	if link.Exposure != "local" || !link.Aligned || len(link.Findings) != 0 {
		t.Fatalf("link = %+v", link)
	}
	if !link.Installed {
		t.Fatalf("link = %+v, want installed", link)
	}
}

func TestDockgeLinkDomain(t *testing.T) {
	srv := dockgeTestServer(t, "other", true)
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dockge?domain=home.arpa", nil)
	srv.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "dockge.home.arpa") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
	// A foreign stack is not Dockge, even when running.
	if strings.Contains(recorder.Body.String(), `"installed":true`) {
		t.Fatalf("body = %s", recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/dockge?domain=Bad+Domain!", nil)
	srv.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("code = %d", recorder.Code)
	}
}

func TestDockgeLinkEngineDown(t *testing.T) {
	srv := New("127.0.0.1:0")
	srv.MountDockge(docker.NewClient(filepath.Join(t.TempDir(), "missing.sock")))
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/dockge", nil))
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("code = %d", recorder.Code)
	}
}
