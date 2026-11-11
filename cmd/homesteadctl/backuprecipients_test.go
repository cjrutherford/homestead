package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func recipientStub(t *testing.T, exports *int) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/backups/recipients":
			_, _ = w.Write([]byte(`{"recipients":[{"fingerprint":"aa","public_key":"age1app","kind":"appliance","label":"appliance identity"}],"owner_held":false,"note":"New runs only."}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/backups/recipients":
			_, _ = w.Write([]byte(`{"fingerprint":"bb","public_key":"age1own","kind":"owner"}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/backups/recipients/bb":
			_, _ = w.Write([]byte(`{"fingerprint":"bb","kind":"owner"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/backups/identity":
			_, _ = w.Write([]byte(`{"recipient":"age1app","fingerprint":"aa","exported":false}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/backups/identity/export":
			*exports++
			_, _ = w.Write([]byte(`{"identity":"AGE-SECRET-KEY-TEST","recipient":"age1app","fingerprint":"aa"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/backups/identity/rotate":
			*exports += 1000
			_, _ = w.Write([]byte(`{"identity":"AGE-SECRET-KEY-NEW","recipient":"age1new","fingerprint":"cc","previous_fingerprint":"aa","rewrap_operation_id":7,"runs_note":"existing runs are being re-wrapped in the background"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/backups/identity/rewrap":
			*exports += 10000
			_, _ = w.Write([]byte(`{"id":7}`))
		case r.URL.Path == "/api/v1/operations/7/events":
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := w.(http.Flusher)
			for _, status := range []string{"running", "succeeded"} {
				fmt.Fprintf(w, "event: state\ndata: {\"id\":7,\"type\":\"backup.rewrap\",\"status\":%q,\"steps\":[]}\n\n", status)
				flusher.Flush()
			}
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/backups/identity/ack":
			*exports += 100
			_, _ = w.Write([]byte(`{"created":true,"exported":true,"acknowledged":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestBackupRecipientsCLI(t *testing.T) {
	n := 0
	url := recipientStub(t, &n)
	code, out, _ := invoke(t, []string{"--server", url, "backup", "recipients", "list"})
	if code != exitOK || !strings.Contains(out, "appliance") || !strings.Contains(out, "warning") {
		t.Fatalf("list %d %q", code, out)
	}
	if code, out, _ = invoke(t, []string{"--server", url, "backup", "recipients", "add", "--key", "age1own"}); code != exitOK || !strings.Contains(out, "bb") {
		t.Fatalf("add %d %q", code, out)
	}
	if code, _, _ = invoke(t, []string{"--server", url, "backup", "recipients", "add"}); code != exitUsage {
		t.Fatalf("add without key = %d", code)
	}
	if code, out, _ = invoke(t, []string{"--server", url, "backup", "recipients", "remove", "--fingerprint", "bb"}); code != exitOK || !strings.Contains(out, "removed") {
		t.Fatalf("remove %d %q", code, out)
	}
}

func TestBackupIdentityExportRequiresExplicitDestination(t *testing.T) {
	n := 0
	url := recipientStub(t, &n)
	if code, _, _ := invoke(t, []string{"--server", url, "backup", "identity", "export"}); code != exitUsage || n != 0 {
		t.Fatalf("bare export code=%d exports=%d", code, n)
	}
	if code, _, _ := invoke(t, []string{"--server", url, "backup", "identity", "export", "--stdout", "--out", "x"}); code != exitUsage || n != 0 {
		t.Fatalf("both flags code=%d", code)
	}
	file := filepath.Join(t.TempDir(), "id.txt")
	code, out, _ := invoke(t, []string{"--server", url, "backup", "identity", "export", "--out", file})
	if code != exitOK || n != 1 || strings.Contains(out, "AGE-SECRET-KEY") {
		t.Fatalf("file export code=%d out=%q", code, out)
	}
	info, err := os.Stat(file)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode: %v %v", info, err)
	}
	if raw, _ := os.ReadFile(file); !strings.Contains(string(raw), "AGE-SECRET-KEY-TEST") {
		t.Fatalf("file = %q", raw)
	}
	// Never overwrites an existing file, and does not export.
	if code, _, _ := invoke(t, []string{"--server", url, "backup", "identity", "export", "--out", file}); code == exitOK || n != 1 {
		t.Fatalf("overwrite code=%d exports=%d", code, n)
	}
	code, out, _ = invoke(t, []string{"--server", url, "backup", "identity", "export", "--stdout"})
	if code != exitOK || !strings.Contains(out, "AGE-SECRET-KEY-TEST") {
		t.Fatalf("stdout export %d %q", code, out)
	}
	if code, out, _ = invoke(t, []string{"--server", url, "backup", "identity", "status"}); code != exitOK || !strings.Contains(out, "never exported") {
		t.Fatalf("status %d %q", code, out)
	}
}

func TestBackupIdentityAckFlows(t *testing.T) {
	n := 0
	url := recipientStub(t, &n)
	if code, _, _ := invoke(t, []string{"--server", url, "backup", "identity", "ack"}); code != exitOK || n != 100 {
		t.Fatalf("ack code=%d n=%d", code, n)
	}
	n = 0
	file := filepath.Join(t.TempDir(), "id.txt")
	code, out, stderr := invoke(t, []string{"--server", url, "backup", "identity", "export", "--out", file})
	if code != exitOK || n != 1 || !strings.Contains(stderr, "identity ack") || strings.Contains(out, "recorded:") {
		t.Fatalf("export without --ack: code=%d n=%d out=%q err=%q", code, n, out, stderr)
	}
	n = 0
	file = filepath.Join(t.TempDir(), "id2.txt")
	if code, out, _ = invoke(t, []string{"--server", url, "backup", "identity", "export", "--out", file, "--ack"}); code != exitOK || n != 101 || !strings.Contains(out, "recorded:") {
		t.Fatalf("export --ack: code=%d n=%d out=%q", code, n, out)
	}
}

func TestBackupIdentityExportRefusesSymlinkTarget(t *testing.T) {
	n := 0
	url := recipientStub(t, &n)
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(victim, link); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := invoke(t, []string{"--server", url, "backup", "identity", "export", "--out", link}); code == exitOK || n != 0 {
		t.Fatalf("symlink target accepted: code=%d exports=%d", code, n)
	}
	if raw, _ := os.ReadFile(victim); string(raw) != "keep" {
		t.Fatalf("victim overwritten: %q", raw)
	}
	dangling := filepath.Join(dir, "dangling")
	if err := os.Symlink(filepath.Join(dir, "nowhere"), dangling); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := invoke(t, []string{"--server", url, "backup", "identity", "export", "--out", dangling}); code == exitOK || n != 0 {
		t.Fatalf("dangling symlink accepted: code=%d", code)
	}
}

func TestBackupIdentityRotateNeedsADestinationAndNeverPrintsTheKeyToAFile(t *testing.T) {
	n := 0
	url := recipientStub(t, &n)
	if code, _, _ := invoke(t, []string{"--server", url, "backup", "identity", "rotate"}); code != exitUsage || n != 0 {
		t.Fatalf("bare rotate code=%d n=%d", code, n)
	}
	file := filepath.Join(t.TempDir(), "new.txt")
	code, out, stderr := invoke(t, []string{"--server", url, "backup", "identity", "rotate", "--out", file})
	if code != exitOK || n != 1000 || strings.Contains(out, "AGE-SECRET-KEY") || strings.Contains(stderr, "AGE-SECRET-KEY") {
		t.Fatalf("rotate code=%d n=%d out=%q err=%q", code, n, out, stderr)
	}
	for _, want := range []string{"rotated from aa to cc", "re-wrapped in the background", "operation: 7", "succeeded", "old one"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q: %q", want, out)
		}
	}
	info, err := os.Stat(file)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file: %v %v", info, err)
	}
	if raw, _ := os.ReadFile(file); !strings.Contains(string(raw), "AGE-SECRET-KEY-NEW") {
		t.Fatalf("file = %q", raw)
	}
	if code, _, _ := invoke(t, []string{"--server", url, "backup", "identity", "rotate", "--out", file}); code == exitOK || n != 1000 {
		t.Fatalf("overwrite code=%d n=%d", code, n)
	}
}

func TestBackupIdentityRewrapStartsAndFollows(t *testing.T) {
	n := 0
	url := recipientStub(t, &n)
	code, out, _ := invoke(t, []string{"--server", url, "backup", "identity", "rewrap"})
	if code != exitOK || n != 10000 || !strings.Contains(out, "operation: 7") || !strings.Contains(out, "succeeded") {
		t.Fatalf("rewrap code=%d n=%d out=%q", code, n, out)
	}
}

func TestBackupIdentityRewrapFailureExitsNonZero(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/backups/identity/rewrap":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":8}`))
		case "/api/v1/operations/8/events":
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := w.(http.Flusher)
			fmt.Fprint(w, "event: state\ndata: {\"id\":8,\"type\":\"backup.rewrap\",\"status\":\"failed\",\"steps\":[]}\n\n")
			flusher.Flush()
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	if code, _, _ := invoke(t, []string{"--server", ts.URL, "backup", "identity", "rewrap"}); code != exitError {
		t.Fatalf("failed re-wrap exit = %d", code)
	}
	conflict := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "a re-wrap of existing backups is already running", http.StatusConflict)
	}))
	t.Cleanup(conflict.Close)
	if code, _, stderr := invoke(t, []string{"--server", conflict.URL, "backup", "identity", "rewrap"}); code == exitOK || !strings.Contains(stderr, "already running") {
		t.Fatalf("conflict exit = %d %q", code, stderr)
	}
}

// The key is on disk before the CLI starts following the re-wrap, so an
// interrupted follow never loses the only copy.
func TestBackupIdentityRotateWritesTheKeyFileBeforeFollowing(t *testing.T) {
	file := filepath.Join(t.TempDir(), "new.txt")
	followed := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/backups/identity/rotate":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"identity":"AGE-SECRET-KEY-NEW","recipient":"age1new","fingerprint":"cc","previous_fingerprint":"aa","rewrap_operation_id":9,"runs_note":"in the background"}`))
		case "/api/v1/operations/9/events":
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := w.(http.Flusher)
			fmt.Fprint(w, "event: state\ndata: {\"id\":9,\"type\":\"backup.rewrap\",\"status\":\"running\",\"steps\":[]}\n\n")
			flusher.Flush()
			close(followed)
			<-r.Context().Done() // never ends on its own
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-followed
		cancel()
	}()
	var out, errOut strings.Builder
	code := run(ctx, []string{"--server", ts.URL, "backup", "identity", "rotate", "--out", file}, &out, &errOut)
	if code != exitOK {
		t.Fatalf("code=%d err=%q", code, errOut.String())
	}
	if raw, _ := os.ReadFile(file); !strings.Contains(string(raw), "AGE-SECRET-KEY-NEW") {
		t.Fatalf("key file = %q", raw)
	}
	if strings.Contains(out.String()+errOut.String(), "AGE-SECRET-KEY") {
		t.Fatal("the key was printed")
	}
}

func TestBackupIdentityRotateErrorSaysNotToRotateAgain(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(ts.Close)
	file := filepath.Join(t.TempDir(), "k.txt")
	code, _, stderr := invoke(t, []string{"--server", ts.URL, "backup", "identity", "rotate", "--out", file})
	if code == exitOK || !strings.Contains(stderr, "do not rotate again") {
		t.Fatalf("code=%d err=%q", code, stderr)
	}
	if _, err := os.Stat(file); err == nil {
		t.Fatal("an empty key file was left behind")
	}
}

func TestBackupIdentityStatusShowsRewrapAndRetired(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"created":true,"recipient":"age1x","fingerprint":"cc","exported":false,"retired_count":2,"retired_unreadable":1,"rewrap":{"fingerprint":"cc","finished_at":"2026-11-11T00:00:00Z","failed":["a.age"],"failed_count":1,"complete":false}}`))
	}))
	t.Cleanup(ts.Close)
	code, out, _ := invoke(t, []string{"--server", ts.URL, "backup", "identity", "status"})
	for _, want := range []string{"2 readable, 1 unreadable", "1 file(s) not re-wrapped", "a.age"} {
		if code != exitOK || !strings.Contains(out, want) {
			t.Errorf("status missing %q: %d %q", want, code, out)
		}
	}
}
