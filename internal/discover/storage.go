package discover

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// Production source paths for mount discovery.
const (
	mountinfoPath = "/proc/self/mountinfo"
	diskByUUID    = "/dev/disk/by-uuid"
	diskByLabel   = "/dev/disk/by-label"
)

// Mount is one mounted filesystem. UUID/Label are the stable identifiers:
// kernel mount IDs are session-scoped, but UUIDs persist across reboots
// and device reordering. Virtual filesystems report empty identifiers.
type Mount struct {
	ID         int
	Device     string
	Mountpoint string
	FSType     string
	Options    []string
	ReadOnly   bool
	UUID       string
	Label      string
	Accessible bool
	TotalBytes uint64
	FreeBytes  uint64
	AvailBytes uint64
}

// Storage is the S043 fact set: every mounted filesystem with usage.
// Selection and provisioning belong to later slices (S140+); this only
// observes, and never formats or partitions anything (ADR-013).
type Storage struct {
	Mounts []Mount
}

// DiscoverStorage enumerates mounts with identifiers and usage. Unreadable
// usage (stale network mounts) marks the mount inaccessible instead of
// failing; a missing mount table fails loudly.
func DiscoverStorage() (Storage, error) {
	data, err := os.ReadFile(mountinfoPath)
	if err != nil {
		return Storage{}, fmt.Errorf("discover: read mountinfo: %w", err)
	}
	mounts := parseMountinfo(data)
	uuids := readDiskLinks(diskByUUID)
	labels := readDiskLinks(diskByLabel)
	for i := range mounts {
		base := filepath.Base(mounts[i].Device)
		mounts[i].UUID = uuids[base]
		mounts[i].Label = labels[base]
		mounts[i].Accessible = fillUsage(&mounts[i])
	}
	sort.Slice(mounts, func(i, j int) bool { return mounts[i].Mountpoint < mounts[j].Mountpoint })
	return Storage{Mounts: mounts}, nil
}

// parseMountinfo parses /proc/self/mountinfo lines:
// id parent major:minor root mountpoint options... - fstype source superoptions.
func parseMountinfo(data []byte) []Mount {
	var mounts []Mount
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		sep := -1
		for i, field := range fields {
			if field == "-" {
				sep = i
				break
			}
		}
		if sep < 6 || len(fields) < sep+4 {
			continue
		}
		id, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		if _, err := strconv.Atoi(fields[1]); err != nil {
			continue
		}
		var options []string
		readOnly := false
		for _, opt := range strings.Split(fields[5], ",") {
			options = append(options, opt)
			if opt == "ro" {
				readOnly = true
			}
		}
		mounts = append(mounts, Mount{
			ID:         id,
			Device:     unescape(fields[sep+2]),
			Mountpoint: unescape(fields[4]),
			FSType:     fields[sep+1],
			Options:    options,
			ReadOnly:   readOnly,
		})
	}
	return mounts
}

// unescape decodes mountinfo octal escapes (spaces as \040).
func unescape(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+4 <= len(s) {
			var code int
			if _, err := fmt.Sscanf(s[i+1:i+4], "%3o", &code); err == nil && code <= 0xFF {
				out.WriteByte(byte(code))
				i += 3
				continue
			}
		}
		out.WriteByte(s[i])
	}
	return out.String()
}

// readDiskLinks maps device basenames (sda1) to link names (UUIDs/labels)
// from a /dev/disk/by-* directory. Missing directories yield nothing.
func readDiskLinks(dir string) map[string]string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return map[string]string{}
	}
	links := map[string]string{}
	for _, entry := range entries {
		target, err := os.Readlink(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		links[filepath.Base(target)] = entry.Name()
	}
	return links
}

// fillUsage stats the mountpoint; false reports an inaccessible mount
// (stale network shares) instead of an error.
func fillUsage(mount *Mount) bool {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(mount.Mountpoint, &stat); err != nil {
		return false
	}
	mount.TotalBytes = stat.Blocks * uint64(stat.Bsize)
	mount.FreeBytes = stat.Bfree * uint64(stat.Bsize)
	mount.AvailBytes = stat.Bavail * uint64(stat.Bsize)
	return true
}

// FindMount returns the mount serving path, preferring the longest
// mountpoint prefix (handles nested and bind mounts).
func (s Storage) FindMount(path string) (Mount, bool) {
	var best *Mount
	for i := range s.Mounts {
		mount := &s.Mounts[i]
		if mount.Mountpoint == "/" || strings.HasPrefix(path, mount.Mountpoint+"/") || path == mount.Mountpoint {
			if best == nil || len(mount.Mountpoint) > len(best.Mountpoint) {
				best = mount
			}
		}
	}
	if best == nil {
		return Mount{}, false
	}
	return *best, true
}
