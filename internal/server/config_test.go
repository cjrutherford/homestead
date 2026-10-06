package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/config"
	"homestead/internal/desired"
	"homestead/internal/schema"
)

func testConfigService(t *testing.T) *config.Service {
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
	return config.New(s, filepath.Join(t.TempDir(), desired.FileName))
}

func mountedServer(t *testing.T) *Server {
	t.Helper()
	srv := New("127.0.0.1:0")
	srv.MountConfig(testConfigService(t))
	return srv
}

func doRequest(t *testing.T, srv *Server, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		reader = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, target, reader)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestConfigGetEmpty(t *testing.T) {
	rec := doRequest(t, mountedServer(t), http.MethodGet, "/api/v1/config", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", rec.Code)
	}
	var body struct {
		Version  int            `json:"version"`
		Settings map[string]any `json:"settings"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Version != desired.CurrentVersion {
		t.Fatalf("version = %d, want %d", body.Version, desired.CurrentVersion)
	}
}

func TestConfigApplyThenGet(t *testing.T) {
	srv := mountedServer(t)
	rec := doRequest(t, srv, http.MethodPut, "/api/v1/config", map[string]any{
		"settings": map[string]any{"hostname": "homestead"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var applied struct {
		Settings map[string]any `json:"settings"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&applied); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if applied.Settings["web_port"] != float64(8080) {
		t.Fatalf("applied = %v, want default web_port persisted", applied.Settings)
	}
	rec = doRequest(t, srv, http.MethodGet, "/api/v1/config", nil)
	var current struct {
		Settings map[string]any `json:"settings"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&current); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if current.Settings["hostname"] != "homestead" {
		t.Fatalf("GET after PUT = %v, want hostname", current.Settings)
	}
}

func TestConfigApplyInvalidIs422AndPersistsNothing(t *testing.T) {
	srv := mountedServer(t)
	rec := doRequest(t, srv, http.MethodPut, "/api/v1/config", map[string]any{
		"settings": map[string]any{"web_port": "lots"},
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("PUT status = %d, want 422", rec.Code)
	}
	var failure struct {
		Valid  bool `json:"valid"`
		Errors []struct {
			Field string `json:"field"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&failure); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if failure.Valid || len(failure.Errors) == 0 {
		t.Fatalf("failure body = %+v, want valid=false with errors", failure)
	}
	rec = doRequest(t, srv, http.MethodGet, "/api/v1/config", nil)
	if !strings.Contains(rec.Body.String(), `"settings":{}`) {
		t.Fatalf("invalid PUT persisted state: %s", rec.Body.String())
	}
}

func TestConfigPlanPreviewsWithoutPersisting(t *testing.T) {
	srv := mountedServer(t)
	rec := doRequest(t, srv, http.MethodPost, "/api/v1/config/plan", map[string]any{
		"settings": map[string]any{"hostname": "planned"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST plan status = %d, want 200", rec.Code)
	}
	var plan struct {
		Valid     bool           `json:"valid"`
		Effective map[string]any `json:"effective"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&plan); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !plan.Valid || plan.Effective["hostname"] != "planned" {
		t.Fatalf("plan = %+v, want valid preview", plan)
	}
	rec = doRequest(t, srv, http.MethodGet, "/api/v1/config", nil)
	if !strings.Contains(rec.Body.String(), `"settings":{}`) {
		t.Fatalf("plan persisted state: %s", rec.Body.String())
	}
}

func TestConfigPlanInvalidIs422(t *testing.T) {
	srv := mountedServer(t)
	rec := doRequest(t, srv, http.MethodPost, "/api/v1/config/plan", map[string]any{
		"settings": map[string]any{"bogus": true},
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST plan status = %d, want 422", rec.Code)
	}
}

func TestConfigMalformedBodyIs400(t *testing.T) {
	srv := mountedServer(t)
	for _, target := range []string{"/api/v1/config/plan", "/api/v1/config"} {
		method := http.MethodPost
		if strings.HasSuffix(target, "/config") {
			method = http.MethodPut
		}
		req := httptest.NewRequest(method, target, strings.NewReader("{oops"))
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s %s status = %d, want 400", method, target, rec.Code)
		}
	}
}

func TestConfigUnknownBodyFieldIs400(t *testing.T) {
	srv := mountedServer(t)
	rec := doRequest(t, srv, http.MethodPut, "/api/v1/config", map[string]any{
		"settingz": map[string]any{},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT status = %d, want 400 for unknown body field", rec.Code)
	}
}

func TestConfigGetMethodNotAllowed(t *testing.T) {
	rec := doRequest(t, mountedServer(t), http.MethodDelete, "/api/v1/config", nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE status = %d, want 405", rec.Code)
	}
}
