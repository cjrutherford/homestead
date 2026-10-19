package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/store"
)

func storageTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	srv := New("127.0.0.1:0")
	srv.MountStorage(s)
	return srv
}

func TestStorageMounts(t *testing.T) {
	srv := storageTestServer(t)
	recorder := serveRequest(t, srv, http.MethodGet, "/api/v1/storage/mounts", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	var mounts []struct {
		Mountpoint string `json:"Mountpoint"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &mounts); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(mounts) == 0 {
		t.Fatal("no mounts discovered")
	}
	found := false
	for _, mount := range mounts {
		if mount.Mountpoint == "/" {
			found = true
		}
	}
	if !found {
		t.Fatalf("mounts = %+v, want /", mounts)
	}
}

func TestStorageRegisterAssignFlow(t *testing.T) {
	srv := storageTestServer(t)
	recorder := serveRequest(t, srv, http.MethodGet, "/api/v1/storage/registrations", nil)
	if recorder.Code != http.StatusOK || strings.TrimSpace(recorder.Body.String()) != "[]" {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/storage/registrations",
		map[string]any{"mountpoint": "/", "label": "root", "actor": "owner"})
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"Label":"root"`) {
		t.Fatalf("body = %s", recorder.Body.String())
	}
	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/storage/assignments",
		map[string]any{"class": "application", "mountpoint": "/", "actor": "owner"})
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	recorder = serveRequest(t, srv, http.MethodGet, "/api/v1/storage/assignments", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), `"Class":"application"`) {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

func TestStorageRefusals(t *testing.T) {
	srv := storageTestServer(t)
	// Phantom paths refuse: validation runs against live discovery.
	recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/storage/registrations",
		map[string]any{"mountpoint": "/no/such/mount", "label": "ghost", "actor": "owner"})
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code = %d", recorder.Code)
	}
	// Unknown classes refuse before touching storage.
	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/storage/assignments",
		map[string]any{"class": "telepathy", "mountpoint": "/", "actor": "owner"})
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("code = %d", recorder.Code)
	}
	// Unregistered mounts refuse assignment.
	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/storage/assignments",
		map[string]any{"class": "application", "mountpoint": "/", "actor": "owner"})
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code = %d", recorder.Code)
	}
	// Malformed bodies refuse.
	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/storage/registrations", nil)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("code = %d", recorder.Code)
	}
}

// impactFixture binds "application" to a temp directory (standing in
// for an old mount) holding an installed package's data, with "/"
// registered as the move target.
func impactFixture(t *testing.T, withData bool) (*Server, *store.Store, string) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	srv := New("127.0.0.1:0")
	srv.MountStorage(s)
	old := t.TempDir()
	if withData {
		if err := os.MkdirAll(filepath.Join(old, "jellyfin"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		`INSERT INTO generated_fingerprints (stack, path, sha256, updated_at) VALUES ('jellyfin', 'compose.yaml', 'x', 'now')`,
		`INSERT INTO storage_assignments (class, mountpoint, actor, created_at) VALUES ('application', '` + old + `', 'owner', '2026-01-01T00:00:00Z')`,
	} {
		if _, err := s.DB().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/storage/registrations",
		map[string]any{"mountpoint": "/", "label": "root", "actor": "owner"})
	if recorder.Code != http.StatusOK {
		t.Fatalf("register: %d %s", recorder.Code, recorder.Body.String())
	}
	return srv, s, old
}

func TestStorageReassignRefusesWithoutAcknowledgement(t *testing.T) {
	srv, s, old := impactFixture(t, true)
	body := map[string]any{"class": "application", "mountpoint": "/", "actor": "owner"}
	recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/storage/assignments", body)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	var impact struct {
		From     string   `json:"from"`
		Packages []string `json:"affected_packages"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &impact); err != nil {
		t.Fatal(err)
	}
	if impact.From != old || len(impact.Packages) != 1 || impact.Packages[0] != "jellyfin" {
		t.Fatalf("impact = %+v", impact)
	}
	var mount string
	_ = s.DB().QueryRow(`SELECT mountpoint FROM storage_assignments WHERE class='application'`).Scan(&mount)
	if mount != old {
		t.Fatalf("assignment changed to %q despite refusal", mount)
	}

	body["acknowledge_impact"] = true
	recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/storage/assignments", body)
	if recorder.Code != http.StatusOK {
		t.Fatalf("acknowledged code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	var action, detail string
	if err := s.DB().QueryRow(`SELECT action, detail FROM audit_entries WHERE action='storage.assign.impact_acknowledged'`).Scan(&action, &detail); err != nil {
		t.Fatalf("audit entry missing: %v", err)
	}
	if !strings.Contains(detail, "jellyfin") {
		t.Fatalf("detail = %q", detail)
	}
}

func TestStorageReassignWithoutInstalledDataNeedsNoAcknowledgement(t *testing.T) {
	srv, _, _ := impactFixture(t, false)
	recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/storage/assignments",
		map[string]any{"class": "application", "mountpoint": "/", "actor": "owner"})
	if recorder.Code != http.StatusOK {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
}

func TestStorageMountsReportRegistrability(t *testing.T) {
	srv := storageTestServer(t)
	recorder := serveRequest(t, srv, http.MethodGet, "/api/v1/storage/mounts", nil)
	var mounts []struct {
		Mountpoint        string `json:"Mountpoint"`
		FSType            string `json:"FSType"`
		Registrable       bool   `json:"registrable"`
		RegistrableReason string `json:"registrable_reason"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &mounts); err != nil {
		t.Fatal(err)
	}
	for _, m := range mounts {
		if m.FSType == "tmpfs" && (m.Registrable || m.RegistrableReason == "") {
			t.Fatalf("tmpfs mount %+v should be unregistrable with a reason", m)
		}
		if !m.Registrable && m.RegistrableReason == "" {
			t.Fatalf("mount %+v missing reason", m)
		}
	}
}

func TestStorageUnassignRefusedThenAllowed(t *testing.T) {
	srv, s, old := impactFixture(t, true)
	recorder := serveRequest(t, srv, http.MethodDelete, "/api/v1/storage/assignments/application", nil)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "jellyfin") {
		t.Fatalf("code = %d (%s), want 409 naming jellyfin", recorder.Code, recorder.Body.String())
	}
	var n int
	_ = s.DB().QueryRow(`SELECT COUNT(*) FROM storage_assignments`).Scan(&n)
	if n != 1 {
		t.Fatal("assignment dropped despite refusal")
	}
	// The app's data folder goes away (uninstall): unassign now succeeds,
	// is audited, and never touches the disk.
	if err := os.Remove(filepath.Join(old, "jellyfin")); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(old, "keep.txt")
	if err := os.WriteFile(marker, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	recorder = serveRequest(t, srv, http.MethodDelete, "/api/v1/storage/assignments/application", nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("unassign touched the disk: %v", err)
	}
	var action string
	if err := s.DB().QueryRow(`SELECT action FROM audit_entries WHERE action='storage.unassign'`).Scan(&action); err != nil {
		t.Fatalf("audit entry missing: %v", err)
	}
	if recorder = serveRequest(t, srv, http.MethodDelete, "/api/v1/storage/assignments/application", nil); recorder.Code != http.StatusNotFound {
		t.Fatalf("second unassign code = %d", recorder.Code)
	}
	if recorder = serveRequest(t, srv, http.MethodDelete, "/api/v1/storage/assignments/bogus", nil); recorder.Code != http.StatusBadRequest {
		t.Fatalf("bogus class code = %d", recorder.Code)
	}
}

func TestStorageUnregister(t *testing.T) {
	srv, s, old := impactFixture(t, true)
	if _, err := s.DB().Exec(`INSERT INTO storage_registrations (mountpoint, label, actor, added_at) VALUES (?, 'old', 'owner', '2026-01-01T00:00:00Z')`, old); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/storage/registrations?mountpoint=" + old
	recorder := serveRequest(t, srv, http.MethodDelete, path, nil)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "jellyfin") || !strings.Contains(recorder.Body.String(), "application") {
		t.Fatalf("code = %d (%s), want 409 naming the app and class", recorder.Code, recorder.Body.String())
	}
	// Unassigned and app gone: allowed, audited, disk untouched.
	if _, err := s.DB().Exec(`DELETE FROM storage_assignments`); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(old, "jellyfin")); err != nil {
		t.Fatal(err)
	}
	recorder = serveRequest(t, srv, http.MethodDelete, path, nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatalf("unregister touched the disk: %v", err)
	}
	var action string
	if err := s.DB().QueryRow(`SELECT action FROM audit_entries WHERE action='storage.unregister'`).Scan(&action); err != nil {
		t.Fatalf("audit entry missing: %v", err)
	}
	if recorder = serveRequest(t, srv, http.MethodDelete, path, nil); recorder.Code != http.StatusNotFound {
		t.Fatalf("second unregister code = %d", recorder.Code)
	}
	if recorder = serveRequest(t, srv, http.MethodDelete, "/api/v1/storage/registrations", nil); recorder.Code != http.StatusBadRequest {
		t.Fatalf("missing mountpoint code = %d", recorder.Code)
	}
}

func TestStorageUnassignRefusesEvenWhenAnotherClassSharesTheMount(t *testing.T) {
	srv, s, old := impactFixture(t, true)
	if _, err := s.DB().Exec(`INSERT INTO storage_assignments (class, mountpoint, actor, created_at) VALUES ('bulk', ?, 'owner', '2026-01-01T00:00:00Z')`, old); err != nil {
		t.Fatal(err)
	}
	recorder := serveRequest(t, srv, http.MethodDelete, "/api/v1/storage/assignments/application", nil)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "jellyfin") {
		t.Fatalf("code = %d (%s), want 409 despite the shared mount", recorder.Code, recorder.Body.String())
	}
	// Reassign keeps its exemption: another class on the mount keeps it in play.
	body := map[string]any{"class": "application", "mountpoint": "/", "actor": "owner"}
	if recorder = serveRequest(t, srv, http.MethodPost, "/api/v1/storage/assignments", body); recorder.Code != http.StatusOK {
		t.Fatalf("reassign code = %d (%s)", recorder.Code, recorder.Body.String())
	}
}

func TestStorageUnreadableDiskRefuses(t *testing.T) {
	srv, s, old := impactFixture(t, false)
	gone := filepath.Join(old, "detached")
	if _, err := s.DB().Exec(`UPDATE storage_assignments SET mountpoint = ?`, gone); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`INSERT INTO storage_registrations (mountpoint, label, actor, added_at) VALUES (?, 'gone', 'owner', '2026-01-01T00:00:00Z')`, gone); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ method, path string }{
		{http.MethodDelete, "/api/v1/storage/assignments/application"},
		{http.MethodDelete, "/api/v1/storage/registrations?mountpoint=" + gone},
	} {
		recorder := serveRequest(t, srv, c.method, c.path, nil)
		if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "is not readable; reattach it before changing storage") {
			t.Fatalf("%s: code = %d (%s)", c.path, recorder.Code, recorder.Body.String())
		}
	}
	// A reassign off the unreadable disk refuses too.
	body := map[string]any{"class": "application", "mountpoint": "/", "actor": "owner"}
	if recorder := serveRequest(t, srv, http.MethodPost, "/api/v1/storage/assignments", body); recorder.Code != http.StatusConflict {
		t.Fatalf("reassign code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	var n int
	_ = s.DB().QueryRow(`SELECT COUNT(*) FROM storage_assignments`).Scan(&n)
	if n != 1 {
		t.Fatal("assignment changed despite refusal")
	}
}

func TestStorageUnassignUsesDatabaseEvidenceOnAnEmptyMount(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	srv := New("127.0.0.1:0")
	srv.MountStorage(s, "../../catalog")
	empty := t.TempDir() // the disk is detached: an empty mountpoint directory
	for _, q := range []string{
		`INSERT INTO generated_fingerprints (stack, path, sha256, updated_at) VALUES ('jellyfin', 'compose.yaml', 'x', 'now')`,
		`INSERT INTO storage_assignments (class, mountpoint, actor, created_at) VALUES ('application', '` + empty + `', 'owner', '2026-01-01T00:00:00Z')`,
	} {
		if _, err := s.DB().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	recorder := serveRequest(t, srv, http.MethodDelete, "/api/v1/storage/assignments/application", nil)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "jellyfin") {
		t.Fatalf("code = %d (%s), want 409 from the installed-app record", recorder.Code, recorder.Body.String())
	}
}

func TestStorageAuditFailureLeavesNothingChanged(t *testing.T) {
	srv, s, old := impactFixture(t, false)
	if _, err := s.DB().Exec(`INSERT INTO storage_registrations (mountpoint, label, actor, added_at) VALUES (?, 'old', 'owner', '2026-01-01T00:00:00Z')`, old); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`CREATE TRIGGER no_audit BEFORE INSERT ON audit_entries BEGIN SELECT RAISE(ABORT, 'audit down'); END`); err != nil {
		t.Fatal(err)
	}
	recorder := serveRequest(t, srv, http.MethodDelete, "/api/v1/storage/assignments/application", nil)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("unassign code = %d", recorder.Code)
	}
	var n int
	_ = s.DB().QueryRow(`SELECT COUNT(*) FROM storage_assignments`).Scan(&n)
	if n != 1 {
		t.Fatal("unassign applied although the audit failed")
	}
	if _, err := s.DB().Exec(`DELETE FROM storage_assignments`); err != nil {
		t.Fatal(err)
	}
	recorder = serveRequest(t, srv, http.MethodDelete, "/api/v1/storage/registrations?mountpoint="+old, nil)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("unregister code = %d (%s)", recorder.Code, recorder.Body.String())
	}
	_ = s.DB().QueryRow(`SELECT COUNT(*) FROM storage_registrations WHERE mountpoint = ?`, old).Scan(&n)
	if n != 1 {
		t.Fatal("unregister applied although the audit failed")
	}
}
