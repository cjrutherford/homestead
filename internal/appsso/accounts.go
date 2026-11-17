package appsso

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"homestead/internal/catalog"

	_ "modernc.org/sqlite"
)

// AppAccounts are the usernames an app already has (S418): the forward-auth
// plan refuses a collision with a person, and an app with no administrator.
// It holds names and a count, never a password or token.
type AppAccounts struct {
	// Names are the usernames, lower-cased, sorted, unique.
	Names []string `json:"names"`
	// Admins counts the accounts with the administrator role.
	Admins int `json:"admins"`
	// Err is why the accounts could not be read; the plan then refuses.
	Err string `json:"error,omitempty"`
}

// ReadAccounts reads the app's accounts for a forward-auth manifest. A
// variable so tests and the server's stubs skip Docker.
var ReadAccounts = func(ctx context.Context, m catalog.Manifest) AppAccounts {
	fa := m.SSO.ForwardAuth
	if fa == nil || fa.AccountsFrom != catalog.AccountsNavidromeSQLite {
		return AppAccounts{Err: "no reader for this app's accounts"}
	}
	return readNavidromeAccounts(ctx, container(m, fa.Service), "/data/navidrome.db")
}

// copyFromContainer copies one file out of a container, stubbed in tests.
var copyFromContainer = func(ctx context.Context, container, src, dst string) error {
	if out, err := exec.CommandContext(ctx, "docker", "cp", container+":"+src, dst).CombinedOutput(); err != nil {
		return fmt.Errorf("copy from %s: %w: %s", container, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// readNavidromeAccounts copies Navidrome's database (and its write-ahead
// log, when there is one) out of the container and reads the user table
// from the copy. The running app is never opened or written.
func readNavidromeAccounts(ctx context.Context, container, dbPath string) AppAccounts {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	dir, err := os.MkdirTemp("", "homestead-accounts-")
	if err != nil {
		return AppAccounts{Err: "no scratch space"}
	}
	defer os.RemoveAll(dir)
	dst := filepath.Join(dir, "app.db")
	if err := copyFromContainer(ctx, container, dbPath, dst); err != nil {
		return AppAccounts{Err: "the database could not be copied from the container"}
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		// Optional: absent when the database is checkpointed.
		_ = copyFromContainer(ctx, container, dbPath+suffix, dst+suffix)
	}
	return queryNavidromeUsers(ctx, dst)
}

func queryNavidromeUsers(ctx context.Context, path string) AppAccounts {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout%285000%29")
	if err != nil {
		return AppAccounts{Err: "the database could not be opened"}
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT user_name, is_admin FROM user`)
	if err != nil {
		return AppAccounts{Err: "the user table could not be read"}
	}
	defer rows.Close()
	out := AppAccounts{Names: []string{}}
	seen := map[string]bool{}
	for rows.Next() {
		var name string
		var admin bool
		if err := rows.Scan(&name, &admin); err != nil {
			return AppAccounts{Err: "the user table could not be read"}
		}
		if admin {
			out.Admins++
		}
		if n := strings.ToLower(strings.TrimSpace(name)); n != "" && !seen[n] {
			seen[n] = true
			out.Names = append(out.Names, n)
		}
	}
	if rows.Err() != nil {
		return AppAccounts{Err: "the user table could not be read"}
	}
	sort.Strings(out.Names)
	return out
}
