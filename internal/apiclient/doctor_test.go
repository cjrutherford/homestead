package apiclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"homestead/internal/server"
)

func TestDoctorRoundTrip(t *testing.T) {
	srv := server.New("127.0.0.1:0")
	srv.MountDoctor()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	report, err := New(ts.URL).Doctor(context.Background())
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if len(report.Checks) == 0 {
		t.Fatal("empty checks")
	}
	if report.Overall == "" {
		t.Fatal("empty overall")
	}
}

func TestDoctorLiveSendsFlag(t *testing.T) {
	var query string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"overall":"ok","live":true,"checked_at":"2026-01-01T00:00:00Z","checks":[]}`))
	}))
	defer ts.Close()
	report, err := New(ts.URL).DoctorLive(context.Background())
	if err != nil || query != "live=1" || !report.Live || report.CheckedAt == "" {
		t.Fatalf("%v %q %+v", err, query, report)
	}
}
