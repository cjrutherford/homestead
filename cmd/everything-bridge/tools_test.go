package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
