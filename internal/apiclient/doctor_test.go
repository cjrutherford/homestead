package apiclient

import (
	"context"
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
