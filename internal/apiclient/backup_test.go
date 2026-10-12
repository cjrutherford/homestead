package apiclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBackupUnscheduleEscapesPackageName(t *testing.T) {
	var got string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.EscapedPath()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(ts.Close)
	if err := New(ts.URL).BackupUnschedule(context.Background(), "a/b c?x"); err != nil {
		t.Fatal(err)
	}
	if got != "/api/v1/backups/schedules/a%2Fb%20c%3Fx" {
		t.Fatalf("path = %q", got)
	}
}
