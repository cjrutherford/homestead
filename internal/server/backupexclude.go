package server

import (
	"fmt"
	"path"

	"homestead/internal/backup"
	"homestead/internal/catalog"
	"homestead/internal/storage"
)

// applyDatabaseHandling decides what a file-tree backup does with the
// package's declared database directories (S276 `database: true`), which
// cannot be copied consistently while the database runs (Postgres drops
// and rewrites files such as pg_internal.init mid-copy).
//
// With a declared export the export is authoritative: those directories
// are excluded from the file copy (the directory itself is kept so its
// mode and owner survive) and the run records that. Without one the
// directories are still copied, vanished files are tolerated, and the
// run records that the copy is not crash-consistent. It returns the
// sources with exclusions applied and the manifest warnings.
func applyDatabaseHandling(manifest catalog.Manifest, mountpoints map[storage.Class]string, sources []backup.Source) ([]backup.Source, []string) {
	out := append([]backup.Source(nil), sources...)
	var notes []string
	seen := map[string]bool{}
	for _, service := range manifest.Services {
		for _, data := range service.Data {
			if !data.Database || data.Ephemeral {
				continue
			}
			sub := path.Clean(data.Subpath)
			key := string(data.Class) + "\x00" + sub
			if sub == "." || sub == ".." || seen[key] {
				continue
			}
			seen[key] = true
			index := -1
			for i := range out {
				if out[i].Mountpoint == mountpoints[data.Class] && out[i].Path == manifest.Name {
					index = i
				}
			}
			if index < 0 {
				continue
			}
			if manifest.Database != nil {
				out[index].Exclude = append(append([]string(nil), out[index].Exclude...), sub)
				notes = append(notes, fmt.Sprintf("database directory %s (%s) is not copied as files: the %s export is authoritative", sub, data.Target, manifest.Database.Kind))
				continue
			}
			notes = append(notes, fmt.Sprintf("database directory %s (%s) is copied as files while it runs and is not crash-consistent; no database export is declared for this package", sub, data.Target))
		}
	}
	return out, notes
}
