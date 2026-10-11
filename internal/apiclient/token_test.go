package apiclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWithTokenSendsBearerOnEveryCall(t *testing.T) {
	var got []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("Authorization"))
		w.Write([]byte(`{"status":"ok","version":"x"}`))
	}))
	defer ts.Close()
	c := New(ts.URL, WithToken("tok"))
	if c.Token() != "tok" || c.BaseURL() != ts.URL {
		t.Fatalf("accessors: %q %q", c.Token(), c.BaseURL())
	}
	if _, err := c.Healthz(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := New(ts.URL).Healthz(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "Bearer tok" || got[1] != "" {
		t.Fatalf("Authorization headers = %q", got)
	}
}
