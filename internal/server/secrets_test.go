package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homestead/internal/audit"
	"homestead/internal/auth"
	"homestead/internal/operation"
	"homestead/internal/secret"
	"homestead/internal/store"
)

func rekeyRequest(t *testing.T, handler http.HandlerFunc, method string, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/x", bytes.NewReader([]byte(body)))
	req = req.WithContext(context.WithValue(req.Context(), operatorKey{},
		operatorSession{Token: "t", Session: auth.VerifiedSession{Username: "owner"}}))
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

func TestSecretRekeyRoute(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	keyPath := filepath.Join(dir, "secret.key")
	secrets, err := secret.New(st, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := secrets.Set("api_token", "super-secret-value"); err != nil {
		t.Fatal(err)
	}
	rekey := handleSecretRekey(New("127.0.0.1:0"), st, secrets)

	// Unconfirmed requests are refused and change nothing.
	for _, body := range []string{``, `{}`, `{"confirm":false}`} {
		if rec := rekeyRequest(t, rekey, http.MethodPost, body); rec.Code != http.StatusBadRequest {
			t.Fatalf("body %q: code = %d", body, rec.Code)
		}
	}
	if secrets.KeyVersion() != 1 {
		t.Fatal("unconfirmed request re-keyed")
	}

	// Status reports version.
	rec := rekeyRequest(t, handleSecretKeyStatus(secrets), http.MethodGet, "")
	if !strings.Contains(rec.Body.String(), `"version":1`) {
		t.Fatalf("status = %s", rec.Body.String())
	}

	rec = rekeyRequest(t, rekey, http.MethodPost, `{"confirm":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d (%s)", rec.Code, rec.Body.String())
	}
	var started struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &started); err != nil || started.ID == 0 {
		t.Fatalf("body = %s", rec.Body.String())
	}
	done, err := operation.NewRunner(st).Wait(context.Background(), started.ID)
	if err != nil || done.Status != operation.StatusSucceeded {
		t.Fatalf("operation = %+v, %v", done, err)
	}
	if len(done.Steps) != 4 {
		t.Fatalf("steps = %+v", done.Steps)
	}
	if secrets.KeyVersion() != 2 {
		t.Fatalf("version = %d", secrets.KeyVersion())
	}
	if got, err := secrets.Get("api_token"); err != nil || got != "super-secret-value" {
		t.Fatalf("Get = %q, %v", got, err)
	}
	if _, err := os.Stat(keyPath + ".v1"); err == nil {
		t.Fatal("old key file retained")
	}

	// Audit: start plus completion, actor from the session, no key
	// material or secret values.
	var entries []audit.Entry
	for i := 0; i < 100; i++ {
		entries, _ = audit.New(st).List(10)
		if len(entries) >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(entries) != 2 {
		t.Fatalf("audit entries = %+v", entries)
	}
	key, _ := os.ReadFile(keyPath + ".v2")
	for _, e := range entries {
		if e.Actor != "owner" || !strings.HasPrefix(e.Action, "secret.rekey") {
			t.Fatalf("entry = %+v", e)
		}
		if strings.Contains(e.Detail, "super-secret-value") || strings.Contains(e.Detail, string(key)) {
			t.Fatalf("audit leaks material: %+v", e)
		}
	}
}

func TestSecretRekeyRoutesSessionClassified(t *testing.T) {
	for _, pattern := range []string{"GET /api/v1/secrets/key", "POST /api/v1/secrets/rekey"} {
		if class, ok := Classify(pattern); !ok || class != ClassSession {
			t.Fatalf("%s = %v, %v", pattern, class, ok)
		}
	}
}
