package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func dataStub(t *testing.T) (string, *map[string]any) {
	t.Helper()
	posted := map[string]any{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/data-migrations":
			_, _ = w.Write([]byte(`{"apps":[{"package":"nextcloud","state":"needs-migration","findings":[
				{"service":"nextcloud","target":"/var/www/html","state":"needs-migration","kind":"volume","volume":"abc"}]}],
				"old_volumes":[{"volume":"old1","package":"jellyfin","target":"/config","size_bytes":5,"state":"pending-removal"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/data-migrations/nextcloud/plan":
			_, _ = w.Write([]byte(`{"Package":"nextcloud","digest":"d1","items":[{"service":"nextcloud","target":"/var/www/html","source":"/v/_data","dest":"/mnt/nextcloud/html","size_bytes":9}],
				"space":[{"mountpoint":"/mnt","needed_bytes":9,"free_bytes":100,"ok":true}],"old_volumes":[{"name":"abc","size_bytes":9}],
				"estimated_downtime_seconds":42,"notes":["n1"],"backup":{"possible":false,"reason":"no backup destination"}}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/approve"), r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/remove-volumes"):
			_ = json.NewDecoder(r.Body).Decode(&posted)
			_, _ = w.Write([]byte(`{"id":21}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts.URL, &posted
}

func TestDataCommands(t *testing.T) {
	url, posted := dataStub(t)
	code, stdout, _ := invoke(t, []string{"--server", url, "data", "status"})
	if code != exitOK || !strings.Contains(stdout, "nextcloud: needs-migration") || !strings.Contains(stdout, "old volume old1") {
		t.Fatalf("status exit=%d:\n%s", code, stdout)
	}
	code, stdout, _ = invoke(t, []string{"--server", url, "data", "plan", "nextcloud"})
	for _, want := range []string{"digest d1", "/v/_data -> /mnt/nextcloud/html", "about 42 seconds", "NOT possible", "--digest d1"} {
		if code != exitOK || !strings.Contains(stdout, want) {
			t.Fatalf("plan missing %q (exit %d):\n%s", want, code, stdout)
		}
	}
	code, stdout, _ = invoke(t, []string{"--server", url, "data", "approve", "nextcloud", "--digest", "d1", "--acknowledge-no-backup"})
	if code != exitOK || !strings.Contains(stdout, "operation: 21") || (*posted)["digest"] != "d1" || (*posted)["acknowledge_no_backup"] != true {
		t.Fatalf("approve exit=%d posted=%v:\n%s", code, *posted, stdout)
	}
	if code, _, _ := invoke(t, []string{"--server", url, "data", "approve", "nextcloud"}); code != exitUsage {
		t.Fatalf("approve without digest exit = %d", code)
	}
	code, stdout, _ = invoke(t, []string{"--server", url, "data", "remove-volumes", "nextcloud", "abc"})
	if code != exitOK || !strings.Contains(stdout, "operation: 21") {
		t.Fatalf("remove exit=%d:\n%s", code, stdout)
	}
	if code, _, _ := invoke(t, []string{"--server", url, "data"}); code != exitUsage {
		t.Fatalf("usage exit = %d", code)
	}
}
