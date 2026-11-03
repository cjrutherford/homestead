package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homestead/internal/everything"
)

func TestToolsExportImportBackup(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "b.db")
	st, err := everything.OpenStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Put(context.Background(), everything.Scope{App: "tasks", Subject: "alice"}, everything.KindKV, "k", []byte(`{"secret-looking":"value"}`)); err != nil {
		t.Fatal(err)
	}
	st.Close()
	var out, errb bytes.Buffer
	exp := filepath.Join(dir, "exp")
	if c := runTool("export", []string{"-db", db, "-subject", "alice", "-app", "tasks", "-out", exp}, &out, &errb); c != 0 {
		t.Fatal(c, errb.String())
	}
	if strings.Contains(out.String(), "secret-looking") {
		t.Fatal("tool printed stored data")
	}
	db2 := filepath.Join(dir, "b2.db")
	if c := runTool("import", []string{"-db", db2, "-in", exp}, &out, &errb); c != 0 {
		t.Fatal(c, errb.String())
	}
	bk := filepath.Join(dir, "bk.db")
	if c := runTool("backup", []string{"-db", db2, "-out", bk}, &out, &errb); c != 0 {
		t.Fatal(c, errb.String())
	}
	if _, err := os.Stat(bk); err != nil {
		t.Fatal(err)
	}
	if c := runTool("export", []string{"-db", filepath.Join(dir, "nope.db"), "-out", filepath.Join(dir, "e3")}, &out, &errb); c == 0 {
		t.Fatal("missing db must fail")
	}
	if _, err := os.Stat(filepath.Join(dir, "nope.db")); err == nil {
		t.Fatal("export created a data file")
	}
}

func TestToolsPeoplePurge(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "p.db")
	st, err := everything.OpenStore(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range [][2]string{{"s1", "Alice"}, {"s2", "bob"}} {
		if err = st.RecordSignIn(context.Background(), "tasks", p[0], p[1], time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	st.Close()
	var out, errb bytes.Buffer
	run := func(args ...string) int {
		out.Reset()
		errb.Reset()
		return runTool("people", append([]string{"purge"}, args...), &out, &errb)
	}
	if c := run("-db", db, "-app", "tasks", "-username", "@alice"); c != 0 || out.String() != "purged 1 people\n" {
		t.Fatal(c, out.String(), errb.String())
	}
	if c := run("-db", db, "-app", "tasks", "-subject", "s1"); c != 0 || out.String() != "purged 0 people\n" {
		t.Fatalf("repeat: %d %q", c, out.String())
	}
	if c := run("-db", db, "-app", "notes", "-subject", "s2"); c != 0 || out.String() != "purged 0 people\n" {
		t.Fatalf("other app: %d %q", c, out.String())
	}
	if c := run("-db", db, "-app", "tasks", "-subject", "s2"); c != 0 || out.String() != "purged 1 people\n" {
		t.Fatalf("subject: %d %q", c, out.String())
	}
	// Exactly one selector; no created files on typos.
	for _, args := range [][]string{{"-db", db, "-app", "tasks"}, {"-db", db, "-app", "tasks", "-subject", "a", "-username", "b"}} {
		if c := run(args...); c != 2 {
			t.Fatalf("%v: %d", args, c)
		}
	}
	missing := filepath.Join(dir, "none.db")
	if c := run("-db", missing, "-app", "tasks", "-subject", "x"); c == 0 {
		t.Fatal("missing db accepted")
	}
	if _, err := os.Stat(missing); err == nil {
		t.Fatal("purge created a data file")
	}
	if c := runTool("people", []string{"list"}, &out, &errb); c != 2 {
		t.Fatal(c)
	}
}

func TestToolsGrantsListRevokeAndExportImport(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "g.db")
	st, err := everything.OpenStore(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, p := range [][2]string{{"s1", "Alice"}, {"s2", "Bob"}} {
		if err = st.RecordSignIn(ctx, "tasks", p[0], p[1], time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if err = st.Put(ctx, everything.Scope{App: "tasks", Subject: "s1"}, everything.KindKV, "p:x", []byte(`"top-secret-value"`)); err != nil {
		t.Fatal(err)
	}
	g, _, err := st.CreateGrant(ctx, everything.Scope{App: "tasks", Subject: "s1"},
		everything.GrantRequest{Username: "bob", Kind: "kv", Key: "p:", IsPrefix: true, Access: "read"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	var out, errb bytes.Buffer
	run := func(args ...string) int {
		out.Reset()
		errb.Reset()
		return runTool("grants", args, &out, &errb)
	}
	if c := run("list", "-db", db); c != 0 || !strings.Contains(out.String(), g.ID) || !strings.Contains(out.String(), "Alice -> Bob") || !strings.HasSuffix(out.String(), "1 grants\n") || strings.Contains(out.String(), "top-secret") {
		t.Fatal(c, out.String(), errb.String())
	}
	// Export carries the grant; import into a store with the same people restores it.
	exp := filepath.Join(dir, "exp")
	if c := runTool("export", []string{"-db", db, "-out", exp}, &out, &errb); c != 0 {
		t.Fatal(c, errb.String())
	}
	if c := run("revoke", "-db", db, "-app", "notes", "-id", g.ID); c != 0 || out.String() != "revoked 0 grants\n" {
		t.Fatalf("other app: %d %q", c, out.String())
	}
	if c := run("revoke", "-db", db, "-app", "tasks", "-id", g.ID); c != 0 || out.String() != "revoked 1 grants\n" {
		t.Fatalf("revoke: %d %q", c, out.String())
	}
	if c := run("revoke", "-db", db, "-app", "tasks", "-id", g.ID); c != 0 || out.String() != "revoked 0 grants\n" {
		t.Fatalf("again: %d %q", c, out.String())
	}
	if c := run("list", "-db", db); c != 0 || out.String() != "0 grants\n" {
		t.Fatalf("live list: %d %q", c, out.String())
	}
	if c := run("list", "-db", db, "-all"); c != 0 || !strings.Contains(out.String(), "revoked") {
		t.Fatalf("all list: %d %q", c, out.String())
	}
	db2 := filepath.Join(dir, "g2.db")
	st2, _ := everything.OpenStore(db2)
	for _, p := range [][2]string{{"s1", "Alice"}, {"s2", "Bob"}} {
		st2.RecordSignIn(ctx, "tasks", p[0], p[1], time.Now())
	}
	st2.Close()
	if c := runTool("import", []string{"-db", db2, "-in", exp}, &out, &errb); c != 0 || !strings.Contains(out.String(), "restored 1 grants, skipped 0") {
		t.Fatal(c, out.String(), errb.String())
	}
	for _, args := range [][]string{{}, {"nope"}, {"revoke", "-db", db}, {"revoke", "-db", db, "-app", "tasks"}} {
		if c := run(args...); c != 2 {
			t.Fatalf("%v: %d", args, c)
		}
	}
	missing := filepath.Join(dir, "none.db")
	if c := run("list", "-db", missing); c == 0 {
		t.Fatal("missing db accepted")
	}
	if _, err := os.Stat(missing); err == nil {
		t.Fatal("grants tool created a data file")
	}
}
