package update

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homestead/internal/catalog"

	"homestead/internal/operation"
	"homestead/internal/store"
)

func updateStack(t *testing.T, images map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	var body strings.Builder
	body.WriteString("name: demo\nservices:\n")
	for service, image := range images {
		body.WriteString("  " + service + ":\n    image: " + image + "\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(body.String()), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return dir
}

func stubUpdate(t *testing.T, ps string, pullFails bool) *[]string {
	t.Helper()
	var calls []string
	old := execCompose
	execCompose = func(_ context.Context, dir, project string, args ...string) (string, error) {
		joined := strings.Join(args, " ")
		calls = append(calls, joined)
		switch {
		case strings.HasPrefix(joined, "pull"):
			if pullFails {
				return "", errors.New("exit status 1: pull failed")
			}
			return "pulled", nil
		case strings.HasPrefix(joined, "up"):
			return "up", nil
		case strings.HasPrefix(joined, "ps "):
			return ps, nil
		}
		return "", errors.New("unexpected " + joined)
	}
	t.Cleanup(func() { execCompose = old })
	return &calls
}

func updateRequest(dir string) UpdateRequest {
	return UpdateRequest{
		Package:  "demo",
		StackDir: dir,
		Moves: []ServiceUpdate{
			{Service: "web", Current: "example/web:1.0.0", Target: "example/web:2.0.0", Source: SourceRegistry},
		},
		Backup: func(context.Context) error { return nil },
	}
}

func TestRewriteImagesUpdatesEveryMatchingServiceImage(t *testing.T) {
	curated, err := catalog.Load(filepath.Join("..", "..", "catalog"))
	if err != nil {
		t.Fatalf("load curated catalog: %v", err)
	}
	manifest, err := curated.Get("authentik")
	if err != nil {
		t.Fatalf("load Authentik manifest: %v", err)
	}
	generated, err := catalog.Generate(manifest, catalog.Generation{})
	if err != nil {
		t.Fatalf("generate Authentik compose: %v", err)
	}
	compose := generated.ComposeYAML
	dir := t.TempDir()
	path := filepath.Join(dir, composeYAMLName)
	if err := os.WriteFile(path, []byte(compose), 0o640); err != nil {
		t.Fatal(err)
	}
	moves := []ServiceUpdate{
		{Service: "server", Current: "ghcr.io/goauthentik/server:2026.8.3", Target: "ghcr.io/goauthentik/server:2026.9.0", Source: SourceRegistry},
		{Service: "worker", Current: "ghcr.io/goauthentik/server:2026.8.3", Target: "ghcr.io/goauthentik/server:2026.9.0", Source: SourceRegistry},
	}
	if err := rewriteImages(dir, moves, func(m ServiceUpdate) string { return m.Current }, func(m ServiceUpdate) string { return m.Target }); err != nil {
		t.Fatalf("rewriteImages: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	if strings.Count(got, moves[0].Target) != 2 || strings.Contains(got, moves[0].Current) || !strings.Contains(got, "image: docker.io/library/postgres:16-alpine") {
		t.Fatalf("expected both Authentik services updated, unrelated service retained:\n%s", got)
	}
}

func TestRewriteImagesPreservesQuotedYAMLAndSupportsTagDigestTransitions(t *testing.T) {
	const compose = "services:\n  server:\n    image: \"ghcr.io/goauthentik/server:2026.8.3\" # owner note\n  worker:\n    image: 'ghcr.io/goauthentik/server:2026.8.3'\n"
	dir := t.TempDir()
	path := filepath.Join(dir, composeYAMLName)
	if err := os.WriteFile(path, []byte(compose), 0o600); err != nil {
		t.Fatal(err)
	}
	move := ServiceUpdate{Service: "server", Current: "ghcr.io/goauthentik/server:2026.8.3", Target: "ghcr.io/goauthentik/server@sha256:0123456789abcdef"}
	if err := rewriteImages(dir, []ServiceUpdate{move}, func(m ServiceUpdate) string { return m.Current }, func(m ServiceUpdate) string { return m.Target }); err != nil {
		t.Fatalf("tag to digest: %v", err)
	}
	move.Current, move.Target = move.Target, "ghcr.io/goauthentik/server:2026.9.0"
	if err := rewriteImages(dir, []ServiceUpdate{move}, func(m ServiceUpdate) string { return m.Current }, func(m ServiceUpdate) string { return m.Target }); err != nil {
		t.Fatalf("digest to tag: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	if strings.Count(got, move.Target) != 1 || !strings.Contains(got, `image: "`+move.Target+`" # owner note`) || !strings.Contains(got, "image: 'ghcr.io/goauthentik/server:2026.8.3'") {
		t.Fatalf("quoted scalars/comments not preserved:\n%s", got)
	}
}

func TestRewriteImagesNoMatchFailsWithoutWriting(t *testing.T) {
	const compose = "services:\n  server:\n    image: ghcr.io/goauthentik/server:2026.8.3\n"
	dir := t.TempDir()
	path := filepath.Join(dir, composeYAMLName)
	if err := os.WriteFile(path, []byte(compose), 0o640); err != nil {
		t.Fatal(err)
	}
	move := ServiceUpdate{Service: "server", Current: "ghcr.io/goauthentik/server:2026.8.2", Target: "ghcr.io/goauthentik/server:2026.9.0"}
	err := rewriteImages(dir, []ServiceUpdate{move}, func(m ServiceUpdate) string { return m.Current }, func(m ServiceUpdate) string { return m.Target })
	if err == nil {
		t.Fatalf("error = %v, want missing image failure", err)
	}
	raw, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(raw) != compose {
		t.Fatalf("no-match changed compose:\n%s", raw)
	}
}

func openUpdateRunner(t *testing.T) *operation.Runner {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "run.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return operation.NewRunner(s)
}

// driftStore opens a store and records the stack's fingerprints, as an
// install does, so a test can ask whether an update leaves it clean.
func driftStore(t *testing.T, dir string) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "drift.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := catalog.RecordFingerprints(s, "demo", dir); err != nil {
		t.Fatalf("RecordFingerprints: %v", err)
	}
	return s
}

func runUpdate(t *testing.T, req UpdateRequest) operation.Operation {
	t.Helper()
	def, err := UpdateDefinition(req)
	if err != nil {
		t.Fatalf("UpdateDefinition: %v", err)
	}
	runner := openUpdateRunner(t)
	op, err := runner.Start(context.Background(), def, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done, err := runner.Wait(context.Background(), op.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	return done
}

func assertDriftClean(t *testing.T, s *store.Store, dir string) {
	t.Helper()
	res, err := catalog.Scan(s, func(string) string { return dir }, time.Now())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if !res.Clean() {
		t.Fatalf("drift after update: %+v", res)
	}
}

func TestUpdateLeavesDriftClean(t *testing.T) {
	dir := updateStack(t, map[string]string{"web": "example/web:1.0.0"})
	s := driftStore(t, dir)
	stubUpdate(t, "{\"Service\":\"web\",\"State\":\"running\"}\n", false)
	req := updateRequest(dir)
	req.Store = s
	if done := runUpdate(t, req); done.Status != operation.StatusSucceeded {
		t.Fatalf("status = %q (%+v)", done.Status, done)
	}
	assertDriftClean(t, s, dir)
}

func TestRollbackLeavesDriftClean(t *testing.T) {
	dir := updateStack(t, map[string]string{"web": "example/web:1.0.0"})
	s := driftStore(t, dir)
	stubUpdate(t, "{\"Service\":\"web\",\"State\":\"exited\"}\n", false)
	req := updateRequest(dir)
	req.Store = s
	if done := runUpdate(t, req); done.Status != operation.StatusFailed || !strings.Contains(done.Error, "rolled back") {
		t.Fatalf("status = %q (%+v)", done.Status, done)
	}
	assertDriftClean(t, s, dir)
}

func TestUpdateFlow(t *testing.T) {
	dir := updateStack(t, map[string]string{"web": "example/web:1.0.0"})
	stubUpdate(t, "{\"Service\":\"web\",\"State\":\"running\"}\n", false)
	def, err := UpdateDefinition(updateRequest(dir))
	if err != nil {
		t.Fatalf("UpdateDefinition: %v", err)
	}
	if def.Type != "app.update" || len(def.Steps) != 4 ||
		def.Steps[0].Name != "backup" || def.Steps[3].Name != "up" {
		t.Fatalf("def = %+v", def)
	}
	runner := openUpdateRunner(t)
	op, err := runner.Start(context.Background(), def, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done, err := runner.Wait(context.Background(), op.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if done.Status != operation.StatusSucceeded {
		t.Fatalf("status = %q (%+v)", done.Status, done)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(raw), "image: example/web:2.0.0") {
		t.Fatalf("pins not rewritten:\n%s", raw)
	}
	// Without backup hooks the definition shortens honestly.
	bare := updateRequest(dir)
	bare.Backup = nil
	def, err = UpdateDefinition(bare)
	if err != nil {
		t.Fatalf("UpdateDefinition: %v", err)
	}
	if len(def.Steps) != 3 || def.Steps[0].Name != "pull" {
		t.Fatalf("def = %+v", def)
	}
}

func TestUpdateRollback(t *testing.T) {
	dir := updateStack(t, map[string]string{"web": "example/web:1.0.0"})
	// New image never runs: verify fails, rollback rewrites and ups,
	// and the error reports both outcomes.
	stubUpdate(t, "{\"Service\":\"web\",\"State\":\"exited\"}\n", false)
	def, err := UpdateDefinition(updateRequest(dir))
	if err != nil {
		t.Fatalf("UpdateDefinition: %v", err)
	}
	runner := openUpdateRunner(t)
	op, err := runner.Start(context.Background(), def, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done, err := runner.Wait(context.Background(), op.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if done.Status != operation.StatusFailed {
		t.Fatalf("status = %q, want failed", done.Status)
	}
	if !strings.Contains(done.Error, "rolled back") {
		t.Fatalf("error = %q", done.Error)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(raw), "image: example/web:1.0.0") {
		t.Fatalf("pins not restored:\n%s", raw)
	}
}

func TestUpdateRefusals(t *testing.T) {
	dir := updateStack(t, map[string]string{"web": "example/web:2.0.0"})
	if _, err := UpdateDefinition(UpdateRequest{}); err == nil {
		t.Fatal("updated empty request")
	}
	// Compose files must describe the plan: unknown images refuse
	// before pull spends network.
	req := updateRequest(dir)
	req.Moves[0].Current = "example/web:9.9.9"
	def, err := UpdateDefinition(req)
	if err != nil {
		t.Fatalf("UpdateDefinition: %v", err)
	}
	stubUpdate(t, "", false)
	runner := openUpdateRunner(t)
	op, err := runner.Start(context.Background(), def, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done, err := runner.Wait(context.Background(), op.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if done.Status != operation.StatusFailed {
		t.Fatalf("status = %q, want failed", done.Status)
	}
	// Pull failures abort before rewriting anything.
	stubUpdate(t, "", true)
	def, err = UpdateDefinition(updateRequest(updateStack(t, map[string]string{"web": "example/web:1.0.0"})))
	if err != nil {
		t.Fatalf("UpdateDefinition: %v", err)
	}
	op, err = runner.Start(context.Background(), def, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done, err = runner.Wait(context.Background(), op.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if done.Status != operation.StatusFailed {
		t.Fatalf("status = %q, want failed", done.Status)
	}
}

func TestRewriteServiceBoundariesAndFlow(t *testing.T) {
	from := func(m ServiceUpdate) string { return m.Current }
	to := func(m ServiceUpdate) string { return m.Target }
	tests := []struct {
		name, input, want string
		moves             []ServiceUpdate
		fail              bool
	}{
		{"shared unplanned", "services:\n  server: {image: example/web:1}\n  worker: {image: example/web:1}\n", "services:\n  server: {image: example/web:2}\n  worker: {image: example/web:1}\n", []ServiceUpdate{{Service: "server", Current: "example/web:1", Target: "example/web:2"}}, false},
		{"flow digest and quote", "services: {server: {image: 'example/web:1'}, worker: {image: example/web@sha256:abc}} # note\n", "services: {server: {image: 'example/web@sha256:def'}, worker: {image: example/web:2}} # note\n", []ServiceUpdate{{Service: "server", Current: "example/web:1", Target: "example/web@sha256:def"}, {Service: "worker", Current: "example/web@sha256:abc", Target: "example/web:2"}}, false},
		{"drift after valid", "services:\n  server: {image: example/web:1}\n  worker: {image: example/web:other}\n", "", []ServiceUpdate{{Service: "server", Current: "example/web:1", Target: "example/web:2"}, {Service: "worker", Current: "example/web:1", Target: "example/web:2"}}, true},
		{"absent", "services:\n  server: {image: example/web:1}\n", "", []ServiceUpdate{{Service: "missing", Current: "example/web:1", Target: "example/web:2"}}, true},
		{"no match", "services:\n  server: {image: example/web:other}\n", "", []ServiceUpdate{{Service: "server", Current: "example/web:1", Target: "example/web:2"}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, composeYAMLName)
			if err := os.WriteFile(path, []byte(tt.input), 0600); err != nil {
				t.Fatal(err)
			}
			err := rewriteImages(dir, tt.moves, from, to)
			if (err != nil) != tt.fail {
				t.Fatalf("error = %v", err)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			want := tt.want
			if tt.fail {
				want = tt.input
			}
			if string(raw) != want {
				t.Fatalf("got %q want %q", raw, want)
			}
		})
	}
}

func TestRollbackConvergingImages(t *testing.T) {
	dir := updateStack(t, map[string]string{"a": "example/web:1", "b": "example/web:2"})
	moves := []ServiceUpdate{{Service: "a", Current: "example/web:1", Target: "example/web:3"}, {Service: "b", Current: "example/web:2", Target: "example/web:3"}}
	from := func(m ServiceUpdate) string { return m.Current }
	to := func(m ServiceUpdate) string { return m.Target }
	if err := rewriteImages(dir, moves, from, to); err != nil {
		t.Fatal(err)
	}
	if err := rewriteImages(dir, moves, to, from); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, composeYAMLName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "image: example/web:1") || !strings.Contains(string(raw), "image: example/web:2") {
		t.Fatalf("not restored: %s", raw)
	}
}

func TestUpdateReportsRollbackRewriteFailure(t *testing.T) {
	dir := updateStack(t, map[string]string{"web": "example/web:1.0.0"})
	req := updateRequest(dir)
	old := execCompose
	t.Cleanup(func() { execCompose = old })
	ups := 0
	execCompose = func(_ context.Context, _, _ string, args ...string) (string, error) {
		if args[0] == "up" {
			ups++
		}
		if args[0] == "ps" {
			if err := os.WriteFile(filepath.Join(dir, composeYAMLName), []byte("services: {}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			return `{"Service":"web","State":"exited"}`, nil
		}
		return "", nil
	}
	def, err := UpdateDefinition(req)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range def.Steps {
		err = step.Run(context.Background())
		if err != nil {
			break
		}
	}
	if err == nil || !strings.Contains(err.Error(), "rollback rewrite failed") || !strings.Contains(err.Error(), `service "web"`) {
		t.Fatalf("error = %v", err)
	}
	if ups != 1 {
		t.Fatalf("up calls = %d, want 1 after rollback rewrite failure", ups)
	}
}
