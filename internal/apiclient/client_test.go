package apiclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"homestead/internal/config"
	"homestead/internal/desired"
	"homestead/internal/schema"
	"homestead/internal/server"
)

func liveClient(t *testing.T) *Client {
	t.Helper()
	s := schema.Schema{Fields: []schema.Field{
		{
			Name:        "hostname",
			Type:        schema.TypeString,
			Description: "Local friendly name.",
			Required:    true,
		},
		{
			Name:        "web_port",
			Type:        schema.TypeInteger,
			Description: "Dashboard port.",
			Default:     8080,
		},
	}}
	if err := s.ValidateDefinition(); err != nil {
		t.Fatalf("ValidateDefinition: %v", err)
	}
	srv := server.New("127.0.0.1:0")
	srv.MountConfig(config.New(s, filepath.Join(t.TempDir(), desired.FileName)))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return New(ts.URL)
}

func TestHealthzAgainstRealServer(t *testing.T) {
	h, err := liveClient(t).Healthz(context.Background())
	if err != nil {
		t.Fatalf("Healthz: %v", err)
	}
	if h.Status != "ok" || h.Version == "" {
		t.Fatalf("Health = %+v, want ok with version", h)
	}
}

func TestVersionAgainstRealServer(t *testing.T) {
	v, err := liveClient(t).Version(context.Background())
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if v == "" {
		t.Fatal("Version empty")
	}
}

func TestGetApplyRoundTrip(t *testing.T) {
	c := liveClient(t)
	ctx := context.Background()
	got, err := c.GetConfig(ctx)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if len(got.Settings) != 0 {
		t.Fatalf("initial Settings = %v, want empty", got.Settings)
	}
	stored, fieldErrs, err := c.ApplyConfig(ctx, map[string]any{"hostname": "homestead"})
	if err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	if len(fieldErrs) != 0 {
		t.Fatalf("ApplyConfig errors = %v, want none", fieldErrs)
	}
	if stored.Settings["web_port"] != float64(8080) {
		t.Fatalf("stored = %v, want default web_port", stored.Settings)
	}
	after, err := c.GetConfig(ctx)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if after.Settings["hostname"] != "homestead" {
		t.Fatalf("after apply = %v, want hostname", after.Settings)
	}
}

func TestApplyInvalidReturnsFieldErrors(t *testing.T) {
	c := liveClient(t)
	_, fieldErrs, err := c.ApplyConfig(context.Background(), map[string]any{"web_port": "lots"})
	if err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	if len(fieldErrs) == 0 {
		t.Fatal("ApplyConfig accepted invalid candidate, want field errors")
	}
}

func TestPlanDoesNotPersist(t *testing.T) {
	c := liveClient(t)
	ctx := context.Background()
	plan, err := c.PlanConfig(ctx, map[string]any{"hostname": "planned"})
	if err != nil {
		t.Fatalf("PlanConfig: %v", err)
	}
	if !plan.Valid || plan.Effective["hostname"] != "planned" {
		t.Fatalf("Plan = %+v, want valid preview", plan)
	}
	after, err := c.GetConfig(ctx)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if len(after.Settings) != 0 {
		t.Fatalf("plan persisted %v", after.Settings)
	}
}

func TestPlanInvalid(t *testing.T) {
	plan, err := liveClient(t).PlanConfig(context.Background(), map[string]any{"bogus": 1})
	if err != nil {
		t.Fatalf("PlanConfig: %v", err)
	}
	if plan.Valid || len(plan.Errors) == 0 {
		t.Fatalf("Plan = %+v, want invalid with errors", plan)
	}
}

func TestServerErrorMapsToStatusError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer ts.Close()
	_, err := New(ts.URL).Healthz(context.Background())
	var statusErr *StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("err = %v, want *StatusError", err)
	}
	if statusErr.Status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", statusErr.Status)
	}
}

func TestUnreachableHostErrors(t *testing.T) {
	_, err := New("http://127.0.0.1:1").Healthz(context.Background())
	if err == nil {
		t.Fatal("Healthz against closed port succeeded, want error")
	}
}

func TestOptionsApply(t *testing.T) {
	c := New("http://127.0.0.1:8080/", WithTimeout(3*time.Second))
	if c.http.Timeout != 3*time.Second {
		t.Fatalf("Timeout = %v, want 3s", c.http.Timeout)
	}
	if c.base != "http://127.0.0.1:8080" {
		t.Fatalf("base = %q, want trailing slash trimmed", c.base)
	}
}
