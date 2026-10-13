package audit

import (
	"strings"
	"testing"
)

func TestRedactTextScrubsKnownShapes(t *testing.T) {
	cases := []string{
		"login failed password=hunter2 for admin",
		"call failed: API_TOKEN=abc123def456 more",
		"header Authorization: Bearer abcdefgh12345678",
		"identity AGE-SECRET-KEY-1QQQQQQQQQQQQQQQQQQQQ leaked",
		"tunnel eyJhIjoiYWJjZGVmZ2hpamtsbW5vcCIsInQiOiJ4In0 token",
		`env DB_PASSWORD="two words"`,
	}
	for _, in := range cases {
		out := RedactText(in)
		for _, leak := range []string{"hunter2", "abc123def456", "abcdefgh12345678", "QQQQQ", "eyJhIjoi", "two words"} {
			if strings.Contains(out, leak) {
				t.Errorf("RedactText(%q) = %q leaks %q", in, out, leak)
			}
		}
	}
	if got := RedactText("registered secret db for immich"); got != "registered secret db for immich" {
		t.Errorf("names must survive, got %q", got)
	}
}

func TestRedactPlanScrubsSensitiveKeys(t *testing.T) {
	out := RedactPlan(`{"host":"a.lan","password":"p1","nested":{"api_key":"k1","items":[{"token":"t1"}]},"note":"token=zzz"}`)
	for _, leak := range []string{"p1", "k1", "t1", "zzz"} {
		if strings.Contains(out, leak) {
			t.Errorf("plan leaks %q: %s", leak, out)
		}
	}
	if !strings.Contains(out, "a.lan") {
		t.Errorf("non-sensitive values must survive: %s", out)
	}
}

func TestRecordRedactsBeforePersisting(t *testing.T) {
	svc := openAuditStore(t)
	e, err := svc.Record(Entry{
		Actor: "admin", Action: "x", Outcome: OutcomeError,
		Detail: "failed password=hunter2", Plan: `{"secret":"s3cr3t"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.Detail, "hunter2") || strings.Contains(got.Plan, "s3cr3t") {
		t.Fatalf("persisted secret: %+v", got)
	}
}
