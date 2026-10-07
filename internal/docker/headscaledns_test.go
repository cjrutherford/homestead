package docker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/dns"
	"homestead/internal/operation"
)

func dnsStackIn(t *testing.T) Stack {
	t.Helper()
	stack := HeadscaleStack()
	stack.Dir = t.TempDir()
	if err := WriteHeadscaleStack(stack.Dir, "https://hs.example.com"); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	return stack
}

func TestExtraRecordsFor(t *testing.T) {
	records := []dns.Record{
		{Name: "app", Type: dns.RecordA, Value: "100.64.0.10", TTLSeconds: 300},
		{Name: "v6", Type: dns.RecordAAAA, Value: "fd7a::10", TTLSeconds: 300},
		{Name: "docs", Type: dns.RecordCNAME, Value: "example.com"},
		{Name: "_acme", Type: dns.RecordTXT, Value: "token"},
	}
	got, err := ExtraRecordsFor("example.com", records)
	if err != nil {
		t.Fatalf("ExtraRecordsFor: %v", err)
	}
	want := []HeadscaleExtraRecord{
		{Name: "app.example.com", Type: "A", Value: "100.64.0.10"},
		{Name: "v6.example.com", Type: "AAAA", Value: "fd7a::10"},
	}
	if len(got) != len(want) {
		t.Fatalf("extras = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("extra %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if _, err := ExtraRecordsFor("BAD", records); err == nil {
		t.Fatal("bridged bad zone")
	}
}

func TestHeadscaleDNSValidation(t *testing.T) {
	good := HeadscaleDNS{
		MagicDNS:    true,
		BaseDomain:  "tail.example.com",
		Nameservers: []string{"1.1.1.1"},
		Extra:       []HeadscaleExtraRecord{{Name: "app.example.com", Type: "A", Value: "100.64.0.10"}},
	}
	if err := good.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	bare := HeadscaleDNS{}
	if err := bare.Validate(); err != nil {
		t.Fatalf("bare Validate: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*HeadscaleDNS)
		want   string
	}{
		{"bad base", func(s *HeadscaleDNS) { s.BaseDomain = "BAD" }, "lowercase"},
		{"bad nameserver", func(s *HeadscaleDNS) { s.Nameservers = []string{"nope"} }, "usable IP"},
		{"bad extra type", func(s *HeadscaleDNS) {
			s.Extra = []HeadscaleExtraRecord{{Name: "a.example.com", Type: "MX", Value: "x"}}
		}, "A or AAAA"},
		{"type mismatch", func(s *HeadscaleDNS) {
			s.Extra = []HeadscaleExtraRecord{{Name: "a.example.com", Type: "A", Value: "::1"}}
		}, "disagree"},
		{"dup extra", func(s *HeadscaleDNS) {
			s.Extra = append(s.Extra, s.Extra[0])
		}, "duplicate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := good
			tc.mutate(&spec)
			err := spec.Validate()
			if err == nil {
				t.Fatal("accepted invalid spec")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q, want %q", err, tc.want)
			}
		})
	}
}

func TestWriteHeadscaleDNS(t *testing.T) {
	stack := dnsStackIn(t)
	spec := HeadscaleDNS{
		MagicDNS:    true,
		BaseDomain:  "tail.example.com",
		Nameservers: []string{"1.1.1.1", "8.8.8.8"},
		Extra:       []HeadscaleExtraRecord{{Name: "app.example.com", Type: "A", Value: "100.64.0.10"}},
	}
	if err := WriteHeadscaleDNS(stack.Dir, spec); err != nil {
		t.Fatalf("WriteHeadscaleDNS: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(stack.Dir, "config", "config.yaml"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, want := range []string{
		"server_url: https://hs.example.com",
		"listen_addr: 0.0.0.0:8080",
		"magic_dns: true",
		"base_domain: tail.example.com",
		"1.1.1.1",
		"extra_records",
		"app.example.com",
	} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("config missing %q:\n%s", want, content)
		}
	}
	// Second write with empty DNS clears the section, keeps the rest.
	if err := WriteHeadscaleDNS(stack.Dir, HeadscaleDNS{}); err != nil {
		t.Fatalf("clear: %v", err)
	}
	content, err = os.ReadFile(filepath.Join(stack.Dir, "config", "config.yaml"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(content), "server_url: https://hs.example.com") {
		t.Fatalf("deployment keys lost:\n%s", content)
	}
	if strings.Contains(string(content), "extra_records") {
		t.Fatalf("stale extras:\n%s", content)
	}
	// Missing deploy fails; bad specs refuse before disk.
	if err := WriteHeadscaleDNS(t.TempDir(), spec); err == nil {
		t.Fatal("wrote without deploy")
	}
	bad := spec
	bad.BaseDomain = "BAD"
	before, _ := os.ReadFile(filepath.Join(stack.Dir, "config", "config.yaml"))
	if err := WriteHeadscaleDNS(stack.Dir, bad); err == nil {
		t.Fatal("wrote bad spec")
	}
	after, _ := os.ReadFile(filepath.Join(stack.Dir, "config", "config.yaml"))
	if string(before) != string(after) {
		t.Fatal("bad spec touched disk")
	}
}

func TestHeadscaleDNSDefinition(t *testing.T) {
	calls := stubHeadscale(t, true, false, false)
	runner := openCaddyRunner(t)
	stack := dnsStackIn(t)
	spec := HeadscaleDNS{MagicDNS: true, BaseDomain: "tail.example.com"}
	def, err := HeadscaleDNSDefinition(stack, spec)
	if err != nil {
		t.Fatalf("HeadscaleDNSDefinition: %v", err)
	}
	if def.Type != "headscale.dns" || len(def.Steps) != 3 ||
		def.Steps[0].Name != "write-dns" || def.Steps[1].Name != "restart" || def.Steps[2].Name != "verify" {
		t.Fatalf("def = %+v", def)
	}
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
	if !strings.Contains(joined, " restart") || !strings.Contains(joined, "ps --format json") {
		t.Fatalf("calls = %v", *calls)
	}
	if _, err := HeadscaleDNSDefinition(stack, HeadscaleDNS{BaseDomain: "BAD"}); err == nil {
		t.Fatal("planned bad spec")
	}
}
