package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"

	"homestead/internal/audit"
	"homestead/internal/auth"
	"homestead/internal/backup"
	"homestead/internal/doctor"
	"homestead/internal/secret"
	"homestead/internal/store"
)

type recipientEnv struct {
	h    *recipientHandlers
	st   *store.Store
	keys backup.KeyStore
	yaml string
}

func newRecipientEnv(t *testing.T) *recipientEnv {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	secrets, err := secret.New(st, filepath.Join(dir, "key"))
	if err != nil {
		t.Fatal(err)
	}
	yaml := filepath.Join(dir, "backup-recipients.yaml")
	h := &recipientHandlers{s: New("127.0.0.1:0"), rec: backup.NewRecipientStore(st), audit: audit.New(st), secrets: secrets, yaml: yaml}
	return &recipientEnv{h: h, st: st, keys: secrets, yaml: yaml}
}

func TestBackupRecipientRoutes(t *testing.T) {
	e := newRecipientEnv(t)
	owner, _ := age.GenerateX25519Identity()
	pub := owner.Recipient().String()

	rec := opRequest(http.MethodPost, `{"public_key":"bogus"}`, e.h.add, "")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bogus = %d", rec.Code)
	}
	rec = opRequest(http.MethodPost, `{"public_key":"`+pub+`","label":"laptop"}`, e.h.add, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("add = %d %s", rec.Code, rec.Body)
	}
	rec = opRequest(http.MethodPost, `{"public_key":"`+pub+`"}`, e.h.add, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("dup = %d", rec.Code)
	}
	rec = opRequest(http.MethodGet, "", e.h.list, "")
	var view recipientList
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Recipients) != 2 || view.Recipients[0].Kind != "appliance" || !view.OwnerHeld {
		t.Fatalf("view = %+v", view)
	}
	if raw, _ := os.ReadFile(e.yaml); !strings.Contains(string(raw), pub) {
		t.Fatalf("yaml missing recipient: %s", raw)
	}
	// The appliance identity cannot be removed.
	applianceFP := view.Recipients[0].Fingerprint
	req := removeRequest(e, applianceFP)
	if req.Code != http.StatusConflict {
		t.Fatalf("appliance removal = %d", req.Code)
	}
	if got := removeRequest(e, backup.Fingerprint(pub)); got.Code != http.StatusOK {
		t.Fatalf("remove = %d", got.Code)
	}
	if got := removeRequest(e, backup.Fingerprint(pub)); got.Code != http.StatusNotFound {
		t.Fatalf("remove again = %d", got.Code)
	}
	entries, _ := audit.New(e.st).List(100)
	var joined string
	for _, en := range entries {
		joined += en.Action + " " + en.Detail + "\n"
	}
	if !strings.Contains(joined, "backup.recipient.add") || !strings.Contains(joined, pub) || strings.Contains(joined, "AGE-SECRET-KEY") {
		t.Fatalf("audit = %s", joined)
	}
}

func removeRequest(e *recipientEnv, fp string) *httptest.ResponseRecorder {
	return opRequestPath(http.MethodDelete, "", e.h.remove, "fingerprint", fp)
}

func TestIdentityExportIsConfirmedAuditedAndClearsDoctor(t *testing.T) {
	e := newRecipientEnv(t)
	check := backupEncryptionDoctorCheck(e.st)
	if c := check(); c[0].Status != doctor.StatusWarn {
		t.Fatalf("fresh = %+v", c)
	}
	if rec := opRequest(http.MethodPost, `{}`, e.h.export, ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("unconfirmed export = %d", rec.Code)
	}
	if c := check(); c[0].Status != doctor.StatusWarn {
		t.Fatal("refused export cleared the warning")
	}
	rec := opRequest(http.MethodPost, `{"confirm":true}`, e.h.export, "")
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("export = %d %v", rec.Code, rec.Header())
	}
	var out exportResult
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	stored, _ := e.keys.Get(backup.IdentitySecret)
	if out.Identity != stored || !strings.HasPrefix(out.Identity, "AGE-SECRET-KEY-") || out.Actor != "owner" {
		t.Fatalf("export = %+v", out)
	}
	if c := check(); c[0].Status != doctor.StatusOK {
		t.Fatalf("after export = %+v", c)
	}
	// Status route reports the export without the key.
	rec = opRequest(http.MethodGet, "", e.h.identityStatus, "")
	if strings.Contains(rec.Body.String(), "AGE-SECRET-KEY") || !strings.Contains(rec.Body.String(), `"exported":true`) {
		t.Fatalf("status = %s", rec.Body)
	}
	entries, _ := audit.New(e.st).List(100)
	found := false
	for _, en := range entries {
		if strings.Contains(en.Action+en.Detail+en.Plan, "AGE-SECRET-KEY") {
			t.Fatal("secret key in audit log")
		}
		found = found || en.Action == "backup.identity.export"
	}
	if !found {
		t.Fatal("export not audited")
	}
	var dump string
	rows, _ := e.st.DB().Query(`SELECT fingerprint || actor FROM backup_identity_exports`)
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		dump += s
	}
	if strings.Contains(dump, "AGE-SECRET-KEY") {
		t.Fatal("export record holds key material")
	}
}

func TestDoctorClearsWithOwnerRecipient(t *testing.T) {
	e := newRecipientEnv(t)
	owner, _ := age.GenerateX25519Identity()
	opRequest(http.MethodPost, `{"public_key":"`+owner.Recipient().String()+`"}`, e.h.add, "")
	if c := backupEncryptionDoctorCheck(e.st)(); c[0].Status != doctor.StatusOK {
		t.Fatalf("check = %+v", c)
	}
	// Removing the only owner recipient restores the warning.
	removeRequest(e, backup.Fingerprint(owner.Recipient().String()))
	if c := backupEncryptionDoctorCheck(e.st)(); c[0].Status != doctor.StatusWarn {
		t.Fatalf("check = %+v", c)
	}
}

func TestJobRecipientsIncludeOwner(t *testing.T) {
	e := newRecipientEnv(t)
	owner, _ := age.GenerateX25519Identity()
	opRequest(http.MethodPost, `{"public_key":"`+owner.Recipient().String()+`"}`, e.h.add, "")
	got, err := backup.RecipientsWithOwner(e.keys, backup.NewRecipientStore(e.st))
	if err != nil || len(got) != 2 {
		t.Fatalf("recipients = %d %v", len(got), err)
	}
}

func opRequestPath(method, body string, handler http.HandlerFunc, key, value string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/x", strings.NewReader(body))
	req.SetPathValue(key, value)
	req = req.WithContext(context.WithValue(req.Context(), operatorKey{},
		operatorSession{Token: "t", Session: auth.VerifiedSession{Username: "owner"}}))
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}
