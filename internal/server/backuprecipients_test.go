package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"

	"homestead/internal/audit"
	"homestead/internal/auth"
	"homestead/internal/backup"
	"homestead/internal/doctor"
	"homestead/internal/operation"
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
	h := &recipientHandlers{s: New("127.0.0.1:0"), rec: backup.NewRecipientStore(st), audit: audit.New(st), secrets: secrets, yaml: yaml, runner: operation.NewRunner(st)}
	return &recipientEnv{h: h, st: st, keys: secrets, yaml: yaml}
}

func TestBackupRecipientRoutes(t *testing.T) {
	e := newRecipientEnv(t)
	owner, _ := age.GenerateX25519Identity()
	pub := owner.Recipient().String()
	if _, err := backup.ApplianceIdentity(e.keys); err != nil {
		t.Fatal(err)
	}

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
	if c := check(); c[0].Status != doctor.StatusWarn || !strings.Contains(c[0].Detail, "not acknowledged") {
		t.Fatalf("export alone = %+v", c)
	}
	if rec := opRequest(http.MethodPost, ``, e.h.acknowledge, ""); rec.Code != http.StatusOK {
		t.Fatalf("ack = %d %s", rec.Code, rec.Body)
	}
	if c := check(); c[0].Status != doctor.StatusOK {
		t.Fatalf("after ack = %+v", c)
	}
	if rec := opRequest(http.MethodPost, ``, e.h.acknowledge, ""); rec.Code != http.StatusConflict {
		t.Fatalf("second ack = %d", rec.Code)
	}
	// Status route reports the export without the key.
	rec = opRequest(http.MethodGet, "", e.h.identityStatus, "")
	if strings.Contains(rec.Body.String(), "AGE-SECRET-KEY") || !strings.Contains(rec.Body.String(), `"acknowledged":true`) {
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

type failingAudit struct{}

func (failingAudit) Record(audit.Entry) (audit.Entry, error) {
	return audit.Entry{}, errors.New("audit store down")
}

func TestExportFailsClosedWhenAuditFails(t *testing.T) {
	e := newRecipientEnv(t)
	e.h.audit = failingAudit{}
	rec := opRequest(http.MethodPost, `{"confirm":true}`, e.h.export, "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("export with failing audit = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "AGE-SECRET-KEY") {
		t.Fatal("key returned although the audit write failed")
	}
}

func TestReadRoutesDoNotCreateTheApplianceIdentity(t *testing.T) {
	e := newRecipientEnv(t)
	rec := opRequest(http.MethodGet, "", e.h.list, "")
	var view recipientList
	_ = json.Unmarshal(rec.Body.Bytes(), &view)
	if view.ApplianceCreated || len(view.Recipients) != 0 {
		t.Fatalf("view = %+v", view)
	}
	rec = opRequest(http.MethodGet, "", e.h.identityStatus, "")
	if !strings.Contains(rec.Body.String(), `"created":false`) {
		t.Fatalf("status = %s", rec.Body)
	}
	opRequest(http.MethodPost, `{"public_key":"bogus"}`, e.h.add, "")
	removeRequest(e, "nope")
	if _, err := e.keys.Get(backup.IdentitySecret); err == nil {
		t.Fatal("a read or refused write created the appliance identity")
	}
	if rec := opRequest(http.MethodPost, `{"confirm":true}`, e.h.export, ""); rec.Code != http.StatusOK {
		t.Fatalf("export = %d", rec.Code)
	}
	if _, err := e.keys.Get(backup.IdentitySecret); err != nil {
		t.Fatal("export did not create the identity")
	}
}

func TestExportIsRateLimitedAndAudited(t *testing.T) {
	e := newRecipientEnv(t)
	for i := 0; i < backup.MaxExportsPerHour; i++ {
		if rec := opRequest(http.MethodPost, `{"confirm":true}`, e.h.export, ""); rec.Code != http.StatusOK {
			t.Fatalf("export %d = %d", i, rec.Code)
		}
	}
	rec := opRequest(http.MethodPost, `{"confirm":true}`, e.h.export, "")
	if rec.Code != http.StatusTooManyRequests || strings.Contains(rec.Body.String(), "AGE-SECRET-KEY") {
		t.Fatalf("over limit = %d %s", rec.Code, rec.Body)
	}
	entries, _ := audit.New(e.st).List(100)
	refused := false
	for _, en := range entries {
		refused = refused || (en.Action == "backup.identity.export" && en.Outcome == audit.OutcomeError)
	}
	if !refused {
		t.Fatal("refusal not audited")
	}
}

func TestRemovingLastOwnerRecipientIsAllowed(t *testing.T) {
	e := newRecipientEnv(t)
	owner, _ := age.GenerateX25519Identity()
	opRequest(http.MethodPost, `{"public_key":"`+owner.Recipient().String()+`"}`, e.h.add, "")
	if rec := removeRequest(e, backup.Fingerprint(owner.Recipient().String())); rec.Code != http.StatusOK {
		t.Fatalf("remove last owner recipient = %d", rec.Code)
	}
}

func TestRenderFailureAfterCommitIsAuditedSeparately(t *testing.T) {
	e := newRecipientEnv(t)
	e.h.yaml = filepath.Join(t.TempDir(), "missing-dir", "x.yaml")
	owner, _ := age.GenerateX25519Identity()
	rec := opRequest(http.MethodPost, `{"public_key":"`+owner.Recipient().String()+`"}`, e.h.add, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("add = %d %s", rec.Code, rec.Body)
	}
	entries, _ := audit.New(e.st).List(100)
	var addOK, renderErr bool
	for _, en := range entries {
		addOK = addOK || (en.Action == "backup.recipient.add" && en.Outcome == audit.OutcomeOK)
		renderErr = renderErr || (en.Action == "backup.recipient.render" && en.Outcome == audit.OutcomeError)
	}
	if !addOK || !renderErr {
		t.Fatalf("addOK=%v renderErr=%v", addOK, renderErr)
	}
}

func TestDoctorReportsUnreadableExportRecord(t *testing.T) {
	e := newRecipientEnv(t)
	if _, err := e.st.DB().Exec(`DROP TABLE backup_identity_exports`); err != nil {
		t.Fatal(err)
	}
	c := backupEncryptionDoctorCheck(e.st)()
	if c[0].Status != doctor.StatusWarn || !strings.Contains(c[0].Detail, "cannot read the recovery identity export record") {
		t.Fatalf("check = %+v", c)
	}
}

func TestDecodeBodyIsBounded(t *testing.T) {
	big := `{"public_key":"` + strings.Repeat("a", maxJSONBody+10) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(big))
	var body struct {
		PublicKey string `json:"public_key"`
	}
	if err := decodeBody(req, &body); err == nil {
		t.Fatal("oversized body decoded")
	}
	small := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"public_key":"x"}`))
	if err := decodeBody(small, &body); err != nil || body.PublicKey != "x" {
		t.Fatalf("small body: %v", err)
	}
}

func TestExportAndAckRejectCookieWithoutCSRF(t *testing.T) {
	a := newEnforcedApp(t)
	rec := a.setup(t)
	cookie := cookieByName(rec, SessionCookieName)
	csrf := jsonField(t, rec, "csrf_token")
	for _, path := range []string{"/api/v1/backups/identity/export", "/api/v1/backups/identity/ack"} {
		got := a.do(http.MethodPost, path, `{"confirm":true}`, func(r *http.Request) { r.AddCookie(cookie) }).Code
		if got != http.StatusForbidden {
			t.Errorf("%s without CSRF = %d, want 403", path, got)
		}
	}
	code := a.do(http.MethodPost, "/api/v1/backups/identity/export", `{"confirm":true}`, func(r *http.Request) {
		r.AddCookie(cookie)
		r.Header.Set(CSRFHeader, csrf)
	}).Code
	if code == http.StatusForbidden || code == http.StatusUnauthorized {
		t.Errorf("export with CSRF rejected = %d", code)
	}
}

// writeRun makes an encrypted run file under root for the given recipient.
func writeRun(t *testing.T, root, rel string, data string, to age.Recipient) string {
	t.Helper()
	run := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(run), 0o755); err != nil {
		t.Fatal(err)
	}
	w, err := os.Create(run)
	if err != nil {
		t.Fatal(err)
	}
	enc, _ := age.Encrypt(w, to)
	_, _ = enc.Write([]byte(data))
	_ = enc.Close()
	_ = w.Close()
	return run
}

func opensWith(t *testing.T, run string, id age.Identity) string {
	t.Helper()
	f, err := os.Open(run)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r, err := age.Decrypt(f, id)
	if err != nil {
		return ""
	}
	got, _ := io.ReadAll(r)
	return string(got)
}

func waitOp(t *testing.T, e *recipientEnv, id int64) operation.Operation {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done, err := e.h.runner.Wait(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return done
}

func auditText(e *recipientEnv) string {
	entries, _ := audit.New(e.st).List(100)
	var joined string
	for _, en := range entries {
		joined += en.Action + " " + en.Detail + "\n"
	}
	return joined
}

func TestIdentityRotateReturnsTheKeyWithoutWaitingAndRewrapsInTheBackground(t *testing.T) {
	e := newRecipientEnv(t)
	root := t.TempDir()
	e.h.destination = func() (string, error) { return root, nil }
	old, _ := backup.ApplianceIdentity(e.keys)
	oldKey := old.String()
	run := writeRun(t, root, "pkg/run1/data.age", "before the rotation", old.Recipient())
	// The re-wrap holds at its end until released: the response must not wait for it.
	release := make(chan struct{})
	e.h.rewrapReported = func() { <-release }

	if rec := opRequest(http.MethodPost, `{}`, e.h.rotate, ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("unconfirmed rotate = %d", rec.Code)
	}
	if stored, _ := e.keys.Get(backup.IdentitySecret); stored != oldKey {
		t.Fatal("a refused rotation changed the key")
	}
	rec := opRequest(http.MethodPost, `{"confirm":true}`, e.h.rotate, "")
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("rotate = %d %s", rec.Code, rec.Body)
	}
	var out rotateResult
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	stored, _ := e.keys.Get(backup.IdentitySecret)
	if out.Identity != stored || stored == oldKey || out.RewrapOperationID == 0 ||
		out.PreviousFingerprint != backup.Fingerprint(old.Recipient().String()) {
		t.Fatalf("rotate = %+v", out)
	}
	op, err := operation.New(e.st).Get(out.RewrapOperationID)
	if err != nil || op.Type != backup.RewrapOperationType || op.Status != operation.StatusRunning {
		t.Fatalf("the re-wrap is not running in the background: %+v %v", op, err)
	}
	// The pending marker is recorded before the re-wrap finishes.
	if sum, ok, _ := e.h.rec.RewrapLast(); !ok || sum.Complete || sum.Fingerprint != out.Fingerprint {
		t.Fatalf("pending marker = %+v %v", sum, ok)
	}
	// A second re-wrap, and a second rotation, are refused while it runs.
	if rec := opRequest(http.MethodPost, ``, e.h.rewrap, ""); rec.Code != http.StatusConflict {
		t.Fatalf("rewrap during a rewrap = %d", rec.Code)
	}
	if rec := opRequest(http.MethodPost, `{"confirm":true}`, e.h.rotate, ""); rec.Code != http.StatusConflict {
		t.Fatalf("rotate during a rewrap = %d %s", rec.Code, rec.Body)
	}
	close(release)
	done := waitOp(t, e, out.RewrapOperationID)
	if done.Status != operation.StatusSucceeded {
		t.Fatalf("re-wrap = %+v", done)
	}
	fresh, _ := age.ParseX25519Identity(out.Identity)
	if got := opensWith(t, run, fresh); got != "before the rotation" {
		t.Fatalf("run = %q", got)
	}
	if sum, ok, _ := e.h.rec.RewrapLast(); !ok || !sum.Complete || sum.Rewrapped != 1 || sum.OperationID != out.RewrapOperationID {
		t.Fatalf("summary = %+v", sum)
	}
	// No key material in the operation, the audit trail, or the doctor.
	raw, _ := json.Marshal(done)
	if strings.Contains(string(raw), "AGE-SECRET-KEY") || strings.Contains(string(raw), oldKey) || strings.Contains(string(raw), out.Identity) {
		t.Fatalf("operation carries key material: %s", raw)
	}
	if joined := auditText(e); !strings.Contains(joined, "backup.identity.rotate") || strings.Contains(joined, "AGE-SECRET-KEY") || strings.Contains(joined, oldKey) || strings.Contains(joined, out.Identity) {
		t.Fatalf("audit = %s", joined)
	}
	// Recorded as an export awaiting acknowledgement.
	if c := backupEncryptionDoctorCheck(e.st)(); c[0].Status != doctor.StatusWarn {
		t.Fatalf("doctor after rotation = %+v", c)
	}
	if c := backupRewrapDoctorCheck(e.st, e.h.secrets)(); c[0].Status != doctor.StatusOK {
		t.Fatalf("rewrap check after a clean re-wrap = %+v", c)
	}
	if rec := opRequest(http.MethodPost, `{}`, e.h.acknowledge, ""); rec.Code != http.StatusOK {
		t.Fatalf("ack = %d", rec.Code)
	}
}

func TestIdentityRotateIsRefusedWhileTheCurrentKeyIsUnacknowledged(t *testing.T) {
	e := newRecipientEnv(t)
	id, _ := backup.ApplianceIdentity(e.keys)
	if rec := opRequest(http.MethodPost, `{"confirm":true}`, e.h.export, ""); rec.Code != http.StatusOK {
		t.Fatalf("export = %d", rec.Code)
	}
	rec := opRequest(http.MethodPost, `{"confirm":true}`, e.h.rotate, "")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "backup identity export") || strings.Contains(rec.Body.String(), "AGE-SECRET-KEY") {
		t.Fatalf("rotate with an unacknowledged key = %d %s", rec.Code, rec.Body)
	}
	if stored, _ := e.keys.Get(backup.IdentitySecret); stored != id.String() {
		t.Fatal("a refused rotation changed the key")
	}
	// Acknowledged (and within the export budget), the rotation passes.
	if rec := opRequest(http.MethodPost, `{}`, e.h.acknowledge, ""); rec.Code != http.StatusOK {
		t.Fatalf("ack = %d", rec.Code)
	}
	if rec := opRequest(http.MethodPost, `{"confirm":true}`, e.h.rotate, ""); rec.Code != http.StatusOK {
		t.Fatalf("rotate after ack = %d %s", rec.Code, rec.Body)
	}
	// The rotation's own export is unacknowledged: a lost response must not mint a second key.
	if rec := opRequest(http.MethodPost, `{"confirm":true}`, e.h.rotate, ""); rec.Code != http.StatusConflict {
		t.Fatalf("second rotate = %d", rec.Code)
	}
	if !strings.Contains(auditText(e), "refused") {
		t.Fatal("the refusal was not audited")
	}
}

func TestIdentityRotateIsRefusedWhileABackupRuns(t *testing.T) {
	e := newRecipientEnv(t)
	id, _ := backup.ApplianceIdentity(e.keys)
	_, release, err := backup.AcquireRecipients(e.keys, e.h.rec)
	if err != nil {
		t.Fatal(err)
	}
	rec := opRequest(http.MethodPost, `{"confirm":true}`, e.h.rotate, "")
	if rec.Code != http.StatusConflict || strings.Contains(rec.Body.String(), "AGE-SECRET-KEY") {
		t.Fatalf("rotate during a backup = %d %s", rec.Code, rec.Body)
	}
	if stored, _ := e.keys.Get(backup.IdentitySecret); stored != id.String() {
		t.Fatal("a refused rotation changed the key")
	}
	release()
	if rec := opRequest(http.MethodPost, `{"confirm":true}`, e.h.rotate, ""); rec.Code != http.StatusOK {
		t.Fatalf("rotate after the backup = %d %s", rec.Code, rec.Body)
	}
}

func TestIdentityRotateWithoutADestinationLeavesAPendingRewrapTheDoctorReports(t *testing.T) {
	e := newRecipientEnv(t)
	e.h.destination = func() (string, error) { return "", errors.New("not mounted") }
	_, _ = backup.ApplianceIdentity(e.keys)
	rec := opRequest(http.MethodPost, `{"confirm":true}`, e.h.rotate, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate = %d %s", rec.Code, rec.Body)
	}
	var out rotateResult
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.RewrapOperationID != 0 || !strings.Contains(out.RunsNote, "backup identity rewrap") {
		t.Fatalf("rotate = %+v", out)
	}
	sum, ok, _ := e.h.rec.RewrapLast()
	if !ok || sum.Complete || sum.Fingerprint != out.Fingerprint {
		t.Fatalf("summary = %+v %v", sum, ok)
	}
	c := backupRewrapDoctorCheck(e.st, e.h.secrets)()
	if c[0].Name != "backup-rewrap" || c[0].Status != doctor.StatusWarn || !strings.Contains(c[0].Detail, "not been re-wrapped") {
		t.Fatalf("doctor = %+v", c)
	}
	st, err := e.h.statusView()
	if err != nil || st.Rewrap == nil || st.Rewrap.Complete || st.RetiredCount != 1 {
		t.Fatalf("status = %+v %v", st, err)
	}
}

func TestIdentityRewrapRouteStartsAuditedBackgroundWorkWithoutAKey(t *testing.T) {
	e := newRecipientEnv(t)
	// No identity yet: nothing to re-wrap to.
	if rec := opRequest(http.MethodPost, ``, e.h.rewrap, ""); rec.Code != http.StatusConflict {
		t.Fatalf("rewrap without an identity = %d", rec.Code)
	}
	if _, ok, _ := backup.PeekApplianceIdentity(e.keys); ok {
		t.Fatal("the rewrap route created an identity")
	}
	old, _ := backup.ApplianceIdentity(e.keys)
	// No destination: 409 with a clear message.
	rec := opRequest(http.MethodPost, ``, e.h.rewrap, "")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "no backup destination") {
		t.Fatalf("rewrap without a destination = %d %s", rec.Code, rec.Body)
	}
	root := t.TempDir()
	e.h.destination = func() (string, error) { return root, nil }
	run := writeRun(t, root, "pkg/run1/data.age", "kept", old.Recipient())
	// A retired key opens the run; the current one does not yet.
	next, _ := age.GenerateX25519Identity()
	e.keys.SetSystem(backup.RetiredIdentitySecret, old.String())
	e.keys.SetSystem(backup.IdentitySecret, next.String())
	rec = opRequest(http.MethodPost, ``, e.h.rewrap, "")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "AGE-SECRET-KEY") {
		t.Fatalf("rewrap = %d %s", rec.Code, rec.Body)
	}
	var out struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if done := waitOp(t, e, out.ID); done.Status != operation.StatusSucceeded {
		t.Fatalf("re-wrap = %+v", done)
	}
	if got := opensWith(t, run, next); got != "kept" {
		t.Fatalf("run = %q", got)
	}
	if joined := auditText(e); !strings.Contains(joined, "backup.identity.rewrap") || strings.Contains(joined, "AGE-SECRET-KEY") {
		t.Fatalf("audit = %s", joined)
	}
}

func TestDoctorRewrapCheckReportsUnreadableRetiredKeysAndFailedFiles(t *testing.T) {
	e := newRecipientEnv(t)
	check := backupRewrapDoctorCheck(e.st, e.h.secrets)
	if c := check(); c[0].Status != doctor.StatusOK {
		t.Fatalf("fresh = %+v", c)
	}
	if _, ok, _ := backup.PeekApplianceIdentity(e.keys); ok {
		t.Fatal("the check created an identity")
	}
	id, _ := backup.ApplianceIdentity(e.keys)
	e.keys.SetSystem(backup.RetiredIdentitySecret, "garbage")
	if c := check(); c[0].Status != doctor.StatusWarn || !strings.Contains(c[0].Detail, "unreadable") {
		t.Fatalf("unreadable retired = %+v", c)
	}
	spare, _ := age.GenerateX25519Identity()
	e.keys.SetSystem(backup.RetiredIdentitySecret, spare.String())
	fp := backup.Fingerprint(id.Recipient().String())
	_ = e.h.rec.SetRewrapLast(backup.RewrapSummary{Fingerprint: fp, FinishedAt: "2026-11-11T00:00:00Z", Failed: []string{"a.age"}, FailedCount: 1})
	if c := check(); c[0].Status != doctor.StatusWarn || !strings.Contains(c[0].Detail, "a.age") {
		t.Fatalf("failed files = %+v", c)
	}
	// A summary for an older key is not this key's problem.
	_ = e.h.rec.SetRewrapLast(backup.RewrapSummary{Fingerprint: "other", Failed: []string{}})
	if c := check(); c[0].Status != doctor.StatusOK {
		t.Fatalf("stale summary = %+v", c)
	}
	_ = e.h.rec.SetRewrapLast(backup.RewrapSummary{Fingerprint: fp, FinishedAt: "2026-11-11T00:00:00Z", Complete: true, Failed: []string{}})
	if c := check(); c[0].Status != doctor.StatusOK {
		t.Fatalf("complete = %+v", c)
	}
}

func TestRewrapRunsOnlyOneAtATimeAcrossHandlers(t *testing.T) {
	e := newRecipientEnv(t)
	_, _ = backup.ApplianceIdentity(e.keys)
	root := t.TempDir()
	e.h.destination = func() (string, error) { return root, nil }
	release := make(chan struct{})
	var once sync.Once
	e.h.rewrapReported = func() { <-release }
	rec := opRequest(http.MethodPost, ``, e.h.rewrap, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("rewrap = %d %s", rec.Code, rec.Body)
	}
	var out struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec := opRequest(http.MethodPost, ``, e.h.rewrap, ""); rec.Code != http.StatusConflict {
		t.Fatalf("second rewrap = %d", rec.Code)
	}
	once.Do(func() { close(release) })
	waitOp(t, e, out.ID)
}

func TestIdentityRotateWithholdsTheKeyWhenAuditFails(t *testing.T) {
	e := newRecipientEnv(t)
	e.h.audit = failingAudit{}
	rec := opRequest(http.MethodPost, `{"confirm":true}`, e.h.rotate, "")
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "AGE-SECRET-KEY") {
		t.Fatalf("rotate with a failing audit = %d %s", rec.Code, rec.Body)
	}
}

func TestIdentityRotateIsRateLimitedBeforeAnyKeyChanges(t *testing.T) {
	e := newRecipientEnv(t)
	id, _ := backup.ApplianceIdentity(e.keys)
	for i := 0; i < backup.MaxExportsPerHour; i++ {
		if _, err := e.h.rec.RecordExport("fp", "owner"); err != nil {
			t.Fatal(err)
		}
	}
	rec := opRequest(http.MethodPost, `{"confirm":true}`, e.h.rotate, "")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("rotate = %d", rec.Code)
	}
	if stored, _ := e.keys.Get(backup.IdentitySecret); stored != id.String() {
		t.Fatal("a rate-limited rotation changed the key")
	}
}
