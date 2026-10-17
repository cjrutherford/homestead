package server

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"homestead/internal/backup"
	"homestead/internal/catalog"
	"homestead/internal/docker"
	"homestead/internal/storage"
)

// backupInspect reads a package's containers (with mounts) from the
// engine, stubbed in tests.
var backupInspect = func(packageName string) ([]docker.Container, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	all, err := docker.NewClient("").Containers(ctx)
	if err != nil {
		return nil, err
	}
	return catalog.ProjectContainers(all, packageName), nil
}

// assignedMountpoints maps classes to their assigned mounts.
func assignedMountpoints(assignments []storage.Assignment) map[storage.Class]string {
	out := map[storage.Class]string{}
	for _, assignment := range assignments {
		out[assignment.Class] = assignment.Mountpoint
	}
	return out
}

// guardBackupSources is the S277 backup source guard. A backup that
// copies a stray directory, or that misses where the app really writes,
// must fail instead of reporting success. It fails when a declared
// persistent path is missing or outside the chosen sources, or when a
// running container keeps a declared path anywhere the sources cannot
// see (a Docker volume, the container layer, tmpfs, or a bind mount
// elsewhere). It returns warnings for conditions it tolerates and the
// caller records in the run manifest: declared paths that exist but are
// all empty (an app that has not written yet), and image volumes the
// catalog does not declare. Read-only binds and owner-supplied extra
// binds are the owner's libraries and are not judged.
func guardBackupSources(manifest catalog.Manifest, mountpoints map[storage.Class]string, sources []backup.Source, containers []docker.Container) ([]string, error) {
	// Zero persistent sources on a stateless package is a defined result,
	// not a missing-directory failure (ADR-028).
	if manifest.Stateless() {
		return nil, fmt.Errorf("%w: %q declares no persistent data", backup.ErrStateless, manifest.Name)
	}
	var roots []string
	for _, source := range sources {
		roots = append(roots, filepath.Join(source.Mountpoint, source.Path))
	}
	covered := func(path string) bool {
		path = filepath.Clean(path)
		for _, root := range roots {
			if path == root || strings.HasPrefix(path, root+"/") {
				return true
			}
		}
		return false
	}
	mounts, _, err := catalog.ResolveData(manifest, mountpoints)
	if err != nil {
		return nil, fmt.Errorf("backup source guard: %w", err)
	}
	var problems, warnings []string
	haveData := false
	checked := map[string]bool{}
	persistent := 0
	for _, mount := range mounts {
		if mount.Ephemeral {
			continue
		}
		persistent++
		if checked[mount.Source] {
			continue
		}
		checked[mount.Source] = true
		info, err := os.Stat(mount.Source)
		switch {
		case err != nil || !info.IsDir():
			problems = append(problems, fmt.Sprintf("declared data path %s (%s %s) is missing", mount.Source, mount.Service, mount.Target))
			continue
		case !covered(mount.Source):
			problems = append(problems, fmt.Sprintf("declared data path %s (%s %s) is not covered by the backup sources", mount.Source, mount.Service, mount.Target))
			continue
		}
		if holdsFiles(mount.Source) {
			haveData = true
		}
	}
	if persistent > 0 && len(problems) == 0 && !haveData {
		warnings = append(warnings, "every declared data path is empty (the app may not have written yet)")
	}
	if len(containers) > 0 {
		declared := map[string]bool{}
		for _, service := range manifest.Services {
			for _, data := range service.Data {
				declared[service.Name+"\x00"+data.Target] = true
			}
		}
		for _, finding := range catalog.DetectData(manifest, containers, mountpoints).Findings {
			if finding.Target == "" || finding.Ephemeral {
				continue
			}
			isDeclared := declared[finding.Service+"\x00"+finding.Target]
			switch {
			case isDeclared && finding.State == catalog.DataNeedsMigration:
				problems = append(problems, fmt.Sprintf("%s %s is kept in %s, which the backup sources do not cover; migrate the app's data first (docs/architecture/APP_DATA_MIGRATION.md)",
					finding.Service, finding.Target, describeKind(finding)))
			case isDeclared && finding.State == catalog.DataUnknown:
				problems = append(problems, fmt.Sprintf("%s %s: %s", finding.Service, finding.Target, finding.Reason))
			case !isDeclared && finding.Kind == catalog.KindVolume:
				warnings = append(warnings, fmt.Sprintf("%s %s is an image volume (%s) the catalog does not declare; it is not backed up", finding.Service, finding.Target, finding.Volume))
			}
		}
	}
	if len(problems) > 0 {
		return warnings, fmt.Errorf("backup source guard for %q: %s", manifest.Name, strings.Join(problems, "; "))
	}
	return warnings, nil
}

// describeKind names where a needs-migration path currently lives.
func describeKind(finding catalog.DataFinding) string {
	if finding.Kind == catalog.KindLayer {
		return "the container's writable layer"
	}
	return "Docker volume " + finding.Volume
}

// holdsFiles reports whether a tree contains any regular file.
func holdsFiles(root string) bool {
	found := false
	_ = filepath.WalkDir(root, func(_ string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found
}
