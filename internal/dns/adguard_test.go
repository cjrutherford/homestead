package dns

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewAdGuardValidation(t *testing.T) {
	if _, err := NewAdGuard("http://127.0.0.1:3000", "u", "p"); err != nil {
		t.Fatalf("valid: %v", err)
	}
	for _, base := range []string{"", "://bad", "ftp://x/", "http://"} {
		if _, err := NewAdGuard(base, "", ""); err == nil {
			t.Fatalf("accepted %q", base)
		}
	}
}

func TestRewriteValidation(t *testing.T) {
	good := Rewrite{Domain: "app.lan", Answer: "192.168.1.10"}
	if err := good.Validate(); err != nil {
		t.Fatalf("good: %v", err)
	}
	v6 := Rewrite{Domain: "app.lan", Answer: "::1"}
	if err := v6.Validate(); err != nil {
		t.Fatalf("v6: %v", err)
	}
	cases := []struct {
		name string
		r    Rewrite
		want string
	}{
		{"empty domain", Rewrite{Answer: "10.0.0.1"}, "domain"},
		{"uppercase", Rewrite{Domain: "App.lan", Answer: "10.0.0.1"}, "lowercase"},
		{"wildcard", Rewrite{Domain: "*.lan", Answer: "10.0.0.1"}, "domain"},
		{"hostname answer", Rewrite{Domain: "app.lan", Answer: "app"}, "IP address"},
		{"empty answer", Rewrite{Domain: "app.lan"}, "IP address"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.r.Validate()
			if err == nil {
				t.Fatal("accepted invalid rewrite")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q, want %q", err, tc.want)
			}
		})
	}
}

// double serves canned AdGuard control responses, recording calls.
type double struct {
	t      *testing.T
	status int
	body   any
	calls  []string
	auth   []string
}

func (d *double) handler(w http.ResponseWriter, r *http.Request) {
	d.calls = append(d.calls, r.Method+" "+r.URL.Path)
	d.auth = append(d.auth, r.Header.Get("Authorization"))
	w.Header().Set("Content-Type", "application/json")
	if d.status != 0 {
		w.WriteHeader(d.status)
	}
	if d.body != nil {
		if err := json.NewEncoder(w).Encode(d.body); err != nil {
			d.t.Fatalf("encode: %v", err)
		}
	}
}

func serveDouble(t *testing.T, status int, body any) (*Client, *double) {
	t.Helper()
	d := &double{t: t, status: status, body: body}
	srv := httptest.NewServer(http.HandlerFunc(d.handler))
	t.Cleanup(srv.Close)
	c, err := NewAdGuard(srv.URL, "owner", "secret")
	if err != nil {
		t.Fatalf("NewAdGuard: %v", err)
	}
	return c, d
}

func TestFetchStatus(t *testing.T) {
	c, d := serveDouble(t, 0, map[string]any{
		"running": true, "version": "v0.107.79",
		"dns_addresses":      []string{"0.0.0.0"},
		"extra_future_field": 1,
	})
	st, err := c.FetchStatus(context.Background())
	if err != nil {
		t.Fatalf("FetchStatus: %v", err)
	}
	if !st.Running || st.Version != "v0.107.79" || len(st.DNSAddresses) != 1 {
		t.Fatalf("status = %+v", st)
	}
	if d.calls[0] != "GET /control/status" {
		t.Fatalf("calls = %v", d.calls)
	}
	if !strings.HasPrefix(d.auth[0], "Basic ") {
		t.Fatalf("auth = %q", d.auth[0])
	}
}

func TestRewritesRoundTrip(t *testing.T) {
	var stored []map[string]string
	mux := http.NewServeMux()
	mux.HandleFunc("/control/rewrite/list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(stored)
	})
	mux.HandleFunc("/control/rewrite/add", func(w http.ResponseWriter, r *http.Request) {
		var entry map[string]string
		if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
			t.Fatalf("decode: %v", err)
		}
		stored = append(stored, entry)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/control/rewrite/delete", func(w http.ResponseWriter, r *http.Request) {
		var entry map[string]string
		if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
			t.Fatalf("decode: %v", err)
		}
		kept := stored[:0]
		for _, e := range stored {
			if e["domain"] != entry["domain"] {
				kept = append(kept, e)
			}
		}
		stored = kept
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c, err := NewAdGuard(srv.URL, "owner", "secret")
	if err != nil {
		t.Fatalf("NewAdGuard: %v", err)
	}
	ctx := context.Background()
	got, err := c.ListRewrites(ctx)
	if err != nil || len(got) != 0 {
		t.Fatalf("list = %v %v", got, err)
	}
	rw := Rewrite{Domain: "app.lan", Answer: "192.168.1.10"}
	if err := c.AddRewrite(ctx, rw); err != nil {
		t.Fatalf("add: %v", err)
	}
	got, err = c.ListRewrites(ctx)
	if err != nil || len(got) != 1 || got[0] != rw {
		t.Fatalf("list = %v %v", got, err)
	}
	if err := c.DeleteRewrite(ctx, rw); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, err = c.ListRewrites(ctx)
	if err != nil || len(got) != 0 {
		t.Fatalf("list = %v %v", got, err)
	}
	if err := c.AddRewrite(ctx, Rewrite{Domain: "App.lan", Answer: "x"}); err == nil {
		t.Fatal("added invalid rewrite")
	}
}

func TestAdapterErrors(t *testing.T) {
	c, _ := serveDouble(t, http.StatusUnauthorized, map[string]string{"message": "auth"})
	if _, err := c.FetchStatus(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v", err)
	}
	if _, err := c.ListRewrites(context.Background()); err == nil {
		t.Fatal("listed through 401")
	}
	closed, err := NewAdGuard("http://127.0.0.1:1", "", "")
	if err != nil {
		t.Fatalf("NewAdGuard: %v", err)
	}
	if _, err := closed.FetchStatus(context.Background()); err == nil {
		t.Fatal("reached closed port")
	}
}
