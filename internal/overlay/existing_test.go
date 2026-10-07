package overlay

import (
	"context"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/operation"
	"homestead/internal/store"
)

func validExisting() ExistingSetup {
	return ExistingSetup{
		Server:       HeadscaleServer{URL: "https://headscale.example.com"},
		User:         "owner",
		Hostname:     "homestead",
		Routes:       []SubnetRoute{{Destination: netip.MustParsePrefix("192.168.1.0/24"), Enabled: true}},
		AcceptRoutes: true,
	}
}

func TestExistingValidation(t *testing.T) {
	if err := validExisting().Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*ExistingSetup)
		want   string
	}{
		{"bad server", func(s *ExistingSetup) { s.Server.URL = "bogus" }, "http or https"},
		{"bad user", func(s *ExistingSetup) { s.User = "has space" }, "headscale user"},
		{"empty user", func(s *ExistingSetup) { s.User = "" }, "headscale user"},
		{"bad hostname", func(s *ExistingSetup) { s.Hostname = "bad!" }, "invalid hostname"},
		{"bad route", func(s *ExistingSetup) {
			s.Routes = []SubnetRoute{{Destination: netip.MustParsePrefix("0.0.0.0/0"), Enabled: true}}
		}, "exit-node"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setup := validExisting()
			tc.mutate(&setup)
			err := setup.Validate()
			if err == nil {
				t.Fatal("accepted invalid setup")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q, want %q", err, tc.want)
			}
		})
	}
	if _, err := EnrolDefinition(validExisting(), ""); err == nil ||
		!strings.Contains(err.Error(), "must not be empty") {
		t.Fatalf("err = %v", err)
	}
}

func openOverlayRunner(t *testing.T) *operation.Runner {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return operation.NewRunner(s)
}

func TestEnrolFlow(t *testing.T) {
	calls := stubTailscale(t, func(args []string) (string, error) {
		joined := strings.Join(args, " ")
		if strings.HasPrefix(joined, "up ") {
			return "", nil
		}
		return `{"BackendState": "Running",
			"Self": {"HostName": "homestead", "TailscaleIPs": ["100.64.0.1"]},
			"Peer": {}}`, nil
	})
	def, err := EnrolDefinition(validExisting(), "preauth-key")
	if err != nil {
		t.Fatalf("EnrolDefinition: %v", err)
	}
	if def.Type != "headscale.enrol" || len(def.Steps) != 2 ||
		def.Steps[0].Name != "up" || def.Steps[1].Name != "verify" {
		t.Fatalf("def = %+v", def)
	}
	runner := openOverlayRunner(t)
	op, err := runner.Start(context.Background(), def, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done, err := runner.Wait(context.Background(), op.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if done.Status != operation.StatusSucceeded {
		t.Fatalf("status = %q (%+v)", done.Status, done)
	}
	joined := strings.Join(*calls, "\n")
	for _, want := range []string{"--login-server=https://headscale.example.com", "--authkey=preauth-key", "status --json"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("calls = %v, want %q", *calls, want)
		}
	}
}

func TestEnrolVerifyAborts(t *testing.T) {
	stubTailscale(t, func(args []string) (string, error) {
		joined := strings.Join(args, " ")
		if strings.HasPrefix(joined, "up ") {
			return "", nil
		}
		return `{"BackendState": "Stopped", "Self": {}, "Peer": {}}`, nil
	})
	def, err := EnrolDefinition(validExisting(), "preauth-key")
	if err != nil {
		t.Fatalf("EnrolDefinition: %v", err)
	}
	runner := openOverlayRunner(t)
	op, err := runner.Start(context.Background(), def, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done, err := runner.Wait(context.Background(), op.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if done.Status != operation.StatusFailed {
		t.Fatalf("status = %q, want failed", done.Status)
	}
}
