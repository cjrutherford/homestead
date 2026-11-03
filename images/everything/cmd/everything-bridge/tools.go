package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"homestead/internal/everything"
)

// runTool runs the offline subcommands: export, import, backup and the
// container healthcheck. They work on the SQLite file directly, so they need
// neither the daemon nor a running bridge (stop the bridge for import).
// They print counts and paths only, never stored values or session data.
func runTool(cmd string, args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(errw)
	db := fs.String("db", "", "SQLite data file")
	ctx := context.Background()
	fail := func(err error) int { fmt.Fprintln(errw, "everything-bridge "+cmd+":", err); return 1 }
	switch cmd {
	case "export":
		subject := fs.String("subject", "", "export one person (sub claim); empty for everyone")
		app := fs.String("app", "", "export one application; empty for all")
		dir := fs.String("out", "", "empty or new output directory")
		if fs.Parse(args) != nil || *db == "" || *dir == "" {
			return 2
		}
		n, err := everything.Export(ctx, *db, *subject, *app, *dir)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(out, "exported %d objects to %s\n", n, *dir)
	case "import":
		dir := fs.String("in", "", "export directory")
		if fs.Parse(args) != nil || *db == "" || *dir == "" {
			return 2
		}
		st, err := everything.OpenStore(*db)
		if err != nil {
			return fail(err)
		}
		defer st.Close()
		r, err := everything.ImportAll(ctx, st, *dir)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(out, "imported %d objects\n", r.Objects)
		if r.Grants > 0 || r.Skipped > 0 {
			fmt.Fprintf(out, "restored %d grants, skipped %d (people not present)\n", r.Grants, r.Skipped)
		}
	case "backup":
		dst := fs.String("out", "", "new backup file (sessions are not copied)")
		if fs.Parse(args) != nil || *db == "" || *dst == "" {
			return 2
		}
		if err := everything.Backup(ctx, *db, *dst); err != nil {
			return fail(err)
		}
		fmt.Fprintf(out, "backup written to %s\n", *dst)
	case "people":
		// people purge: the owner's (or, in S427, the daemon's) revocation
		// step. It ends the person's sessions in this app and marks them
		// purged; their data stays and remains exportable.
		if len(args) == 0 || args[0] != "purge" {
			fmt.Fprintln(errw, "usage: everything-bridge people purge -db FILE -app APP (-subject SUB | -username NAME)")
			return 2
		}
		app := fs.String("app", "", "application name")
		subject := fs.String("subject", "", "the person's sub claim")
		username := fs.String("username", "", "the person's username (used when no subject)")
		if fs.Parse(args[1:]) != nil || *db == "" || *app == "" || (*subject == "") == (*username == "") {
			return 2
		}
		if _, err := os.Stat(*db); err != nil {
			return fail(err) // never create a data file by typo
		}
		st, err := everything.OpenStore(*db)
		if err != nil {
			return fail(err)
		}
		defer st.Close()
		n, err := st.PurgePerson(ctx, *app, *subject, *username, time.Now())
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(out, "purged %d people\n", n)
	case "grants":
		// grants list|revoke: the appliance owner's view of shares. It prints
		// ids, names and keys, never stored values.
		if len(args) == 0 || (args[0] != "list" && args[0] != "revoke") {
			fmt.Fprintln(errw, "usage: everything-bridge grants list -db FILE [-app APP] [-all] | grants revoke -db FILE -app APP -id ID")
			return 2
		}
		app := fs.String("app", "", "application name")
		id := fs.String("id", "", "grant id (revoke)")
		all := fs.Bool("all", false, "list: include revoked grants")
		if fs.Parse(args[1:]) != nil || *db == "" || (args[0] == "revoke" && (*app == "" || *id == "")) {
			return 2
		}
		if _, err := os.Stat(*db); err != nil {
			return fail(err) // never create a data file by typo
		}
		st, err := everything.OpenStore(*db)
		if err != nil {
			return fail(err)
		}
		defer st.Close()
		if args[0] == "revoke" {
			n, err := st.RevokeGrantByID(ctx, *app, *id, time.Now())
			if err != nil {
				return fail(err)
			}
			fmt.Fprintf(out, "revoked %d grants\n", n)
			break
		}
		gs, err := st.AllGrants(ctx, *app, *all)
		if err != nil {
			return fail(err)
		}
		for _, g := range gs {
			state := "live"
			if g.RevokedAt != 0 {
				state = "revoked"
			}
			fmt.Fprintf(out, "%s\t%s\t%s\t%s\t%s -> %s\t%s\t%s\n", g.ID, g.App, g.Kind, g.Access, g.OwnerName, g.RecipName, g.Key, state)
		}
		fmt.Fprintf(out, "%d grants\n", len(gs))
	case "links":
		// links list|revoke: the appliance owner's view of read links. It
		// prints ids, names, paths and counts; tokens are never stored.
		if len(args) == 0 || (args[0] != "list" && args[0] != "revoke") {
			fmt.Fprintln(errw, "usage: everything-bridge links list -db FILE -app APP [-all] | links revoke -db FILE -app APP -id ID")
			return 2
		}
		app := fs.String("app", "", "application name")
		id := fs.String("id", "", "link id (revoke)")
		all := fs.Bool("all", false, "list: include expired and revoked links")
		if fs.Parse(args[1:]) != nil || *db == "" || *app == "" || (args[0] == "revoke" && *id == "") {
			return 2
		}
		if _, err := os.Stat(*db); err != nil {
			return fail(err) // never create a data file by typo
		}
		st, err := everything.OpenStore(*db)
		if err != nil {
			return fail(err)
		}
		defer st.Close()
		if args[0] == "revoke" {
			n, err := st.RevokeReadLink(ctx, *app, *id, time.Now())
			if err != nil {
				return fail(err)
			}
			fmt.Fprintf(out, "revoked %d links\n", n)
			break
		}
		ls, err := st.ReadLinks(ctx, *app, *all, time.Now())
		if err != nil {
			return fail(err)
		}
		now := time.Now().Unix()
		for _, l := range ls {
			state := "live"
			switch {
			case l.RevokedAt != 0:
				state = "revoked"
			case l.ExpiresAt <= now:
				state = "expired"
			}
			fmt.Fprintf(out, "%s\t%s\t%s\texpires %s\thits %d\t%s\n", l.ID, l.OwnerName, l.Key,
				time.Unix(l.ExpiresAt, 0).UTC().Format(time.RFC3339), l.Hits, state)
		}
		fmt.Fprintf(out, "%d links\n", len(ls))
	case "healthcheck":
		listen := fs.String("listen", envDefault("LISTEN", "127.0.0.1:8081"), "bridge listen address")
		base := fs.String("base-path", envDefault("BASE_PATH", ""), "application path prefix")
		if fs.Parse(args) != nil {
			return 2
		}
		c := http.Client{Timeout: 3 * time.Second}
		res, err := c.Get("http://" + *listen + *base + everything.HealthPath)
		if err != nil {
			return fail(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return fail(fmt.Errorf("status %d", res.StatusCode))
		}
	default:
		fmt.Fprintln(errw, "usage: everything-bridge [serve flags] | export|import|backup|people|grants|links|healthcheck")
		return 2
	}
	return 0
}
