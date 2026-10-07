package apiclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDockerHealth(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/docker/health" || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"reachable":true,"version":"29.1.2","api_version":"1.52","storage_driver":"overlayfs","containers":[{"id":"abc","names":["web"],"image":"nginx","state":"running","status":"Up"}],"images":[],"networks":[]}`))
	}))
	defer ts.Close()
	health, err := New(ts.URL).DockerHealth(context.Background())
	if err != nil {
		t.Fatalf("DockerHealth: %v", err)
	}
	if !health.Reachable || health.Version != "29.1.2" || len(health.Containers) != 1 {
		t.Fatalf("health = %+v", health)
	}
}
