package catalog

import (
	"errors"
	"sync"
)

// ErrMigrationActive refuses work on a package whose data is being
// migrated: the stack is stopped and its files are mid-copy, so
// configure, remove, drift restore, update, and a second migration all
// wait for the owner to see the first one finish.
var ErrMigrationActive = errors.New("catalog: an app data migration is in progress for this package; wait for it to finish")

var (
	migrationMu     sync.Mutex
	migrationActive = map[string]bool{}
)

// acquireMigration claims the package for the whole migration
// operation. The Compose seam lock (lockProject) is per call and not
// reentrant, so the operation-long claim is separate: other operations
// check MigrationActive when they are built.
func acquireMigration(pkg string) (release func(), ok bool) {
	migrationMu.Lock()
	defer migrationMu.Unlock()
	if migrationActive[pkg] {
		return nil, false
	}
	migrationActive[pkg] = true
	return func() {
		migrationMu.Lock()
		delete(migrationActive, pkg)
		migrationMu.Unlock()
	}, true
}

// MigrationActive reports a migration in progress for the package.
func MigrationActive(pkg string) bool {
	migrationMu.Lock()
	defer migrationMu.Unlock()
	return migrationActive[pkg]
}

// RefuseDuringMigration is the guard other operations call when built.
func RefuseDuringMigration(pkg string) error {
	if MigrationActive(pkg) {
		return ErrMigrationActive
	}
	return nil
}
