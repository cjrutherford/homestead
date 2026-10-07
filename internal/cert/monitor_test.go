package cert

import (
	"context"
	"crypto/tls"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func issueFor(t *testing.T, ca Authority, names []string, lifetime time.Duration, at time.Time) []byte {
	t.Helper()
	chain, _, err := ca.IssueServerCert(names, lifetime, at)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	return chain
}

func TestInspectChain(t *testing.T) {
	ca := testCA(t)
	chain := issueFor(t, ca, []string{"app.lan"}, 30*24*time.Hour, testTime())
	seen, err := InspectChain(chain, testTime())
	if err != nil {
		t.Fatalf("InspectChain: %v", err)
	}
	if len(seen.DNSNames) != 1 || seen.DNSNames[0] != "app.lan" {
		t.Fatalf("names = %v", seen.DNSNames)
	}
	if seen.DaysRemaining != 30 || seen.ChainLength != 1 {
		t.Fatalf("observed = %+v", seen)
	}
	if seen.Issuer != "Homestead Internal CA" {
		t.Fatalf("issuer = %q", seen.Issuer)
	}
	expired, err := InspectChain(chain, testTime().Add(31*24*time.Hour))
	if err != nil {
		t.Fatalf("InspectChain: %v", err)
	}
	if expired.DaysRemaining >= 0 {
		t.Fatalf("days = %d", expired.DaysRemaining)
	}
	if _, err := InspectChain([]byte("junk"), testTime()); err == nil {
		t.Fatal("inspected junk")
	}
	if _, err := InspectChain(nil, testTime()); err == nil {
		t.Fatal("inspected empty")
	}
}

func TestFetchChain(t *testing.T) {
	srv := httptest.NewUnstartedServer(nil)
	srv.TLS = &tls.Config{}
	srv.StartTLS()
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "https://")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	chain, err := FetchChain(ctx, addr, "example.com")
	if err != nil {
		t.Fatalf("FetchChain: %v", err)
	}
	seen, err := InspectChain(chain, time.Now())
	if err != nil {
		t.Fatalf("InspectChain: %v", err)
	}
	if seen.ChainLength == 0 {
		t.Fatal("empty chain")
	}
	if _, err := FetchChain(ctx, "", ""); err == nil {
		t.Fatal("fetched empty address")
	}
	if _, err := FetchChain(ctx, "127.0.0.1:1", "example.com"); err == nil {
		t.Fatal("fetched closed port")
	}
}

func fullSet(t *testing.T) (Set, map[string][]byte) {
	now := testTime()
	ca, err := GenerateCA("Monitor CA", DefaultCALifetime, now)
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	healthyNames := []string{"healthy.lan"}
	expiringNames := []string{"expiring.lan"}
	expiredNames := []string{"expired.lan"}
	mismatchNames := []string{"claimed.lan"}
	otherNames := []string{"other.lan"}
	makeCert := func(name string, names []string) Certificate {
		return Certificate{
			Name: name, DNSNames: names,
			Issuer:  Issuer{Name: "ca", Type: IssuerInternal},
			State:   StateIssued,
			Renewal: Renewal{Auto: true},
		}
	}
	set := Set{Certificates: []Certificate{
		makeCert("healthy", healthyNames),
		makeCert("expiring", expiringNames),
		makeCert("expired", expiredNames),
		makeCert("missing", []string{"missing.lan"}),
		makeCert("mismatch", mismatchNames),
		makeCert("broken", []string{"broken.lan"}),
	}}
	observed := map[string][]byte{
		"healthy":  issueFor(t, ca, healthyNames, 300*24*time.Hour, now),
		"expiring": issueFor(t, ca, expiringNames, 10*24*time.Hour, now),
		"expired":  issueFor(t, ca, expiredNames, 10*24*time.Hour, now.Add(-30*24*time.Hour)),
		"mismatch": issueFor(t, ca, otherNames, 300*24*time.Hour, now),
		"broken":   []byte("not pem"),
	}
	return set, observed
}

func TestCheck(t *testing.T) {
	set, observed := fullSet(t)
	statuses, err := Check(set, observed, testTime())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	byName := map[string]Status{}
	for _, status := range statuses {
		byName[status.Name] = status
	}
	want := map[string]string{
		"healthy": HealthHealthy, "expiring": HealthExpiring,
		"expired": HealthExpired, "missing": HealthMissing,
		"mismatch": HealthMismatch, "broken": HealthInvalid,
	}
	for name, health := range want {
		if byName[name].Health != health {
			t.Fatalf("%s = %+v", name, byName[name])
		}
	}
	if !byName["expiring"].RenewalDue || byName["healthy"].RenewalDue {
		t.Fatalf("renewal flags wrong: %+v", byName)
	}
	if byName["healthy"].DaysRemaining != 300 {
		t.Fatalf("days = %d", byName["healthy"].DaysRemaining)
	}
	if !strings.Contains(byName["mismatch"].Detail, "claimed.lan") {
		t.Fatalf("detail = %q", byName["mismatch"].Detail)
	}
	bad := Set{Certificates: []Certificate{func() Certificate {
		c := validCert()
		c.State = "bogus"
		return c
	}()}}
	if _, err := Check(bad, nil, testTime()); err == nil {
		t.Fatal("checked invalid set")
	}
}
