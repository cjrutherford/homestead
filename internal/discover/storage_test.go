package discover

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

const mountinfoFixture = `22 1 8:1 / / rw,relatime shared:1 - ext4 /dev/sda1 rw
23 22 0:5 / /proc rw,nosuid,nodev,noexec,relatime shared:2 - proc proc rw
24 22 8:2 /data /mnt/backup\040old ro,relatime shared:3 - ext4 /dev/sdb1 ro
bad line without separator
25 x 8:3 / /mnt/broken rw shared:4 - ext4 /dev/sdc1 rw
`

func TestParseMountinfo(t *testing.T) {
	mounts := parseMountinfo([]byte(mountinfoFixture))
	if len(mounts) != 3 {
		t.Fatalf("mounts = %+v, want 3 (malformed skipped)", mounts)
	}
	root := mounts[0]
	if root.ID != 22 || root.Device != "/dev/sda1" || root.Mountpoint != "/" || root.FSType != "ext4" {
		t.Fatalf("root = %+v", root)
	}
	if root.ReadOnly {
		t.Fatal("root flagged read-only")
	}
	backup := mounts[2]
	if backup.Mountpoint != "/mnt/backup old" || backup.Device != "/dev/sdb1" {
		t.Fatalf("escaped mount = %+v", backup)
	}
	if !backup.ReadOnly {
		t.Fatal("ro mount not flagged read-only")
	}
}

func TestReadDiskLinks(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "target"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	target := filepath.Join(dir, "target", "sda1")
	if err := os.WriteFile(target, []byte{}, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	byUUID := filepath.Join(dir, "by-uuid")
	if err := os.MkdirAll(byUUID, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(byUUID, "1234-ABCD")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	links := readDiskLinks(byUUID)
	if links["sda1"] != "1234-ABCD" {
		t.Fatalf("links = %v", links)
	}
	if got := readDiskLinks(filepath.Join(dir, "missing")); len(got) != 0 {
		t.Fatalf("missing dir = %v, want empty", got)
	}
}

func TestFillUsage(t *testing.T) {
	mount := Mount{Mountpoint: t.TempDir()}
	if !fillUsage(&mount) {
		t.Fatal("tempdir reported inaccessible")
	}
	if mount.TotalBytes == 0 || mount.FreeBytes > mount.TotalBytes {
		t.Fatalf("usage = %+v", mount)
	}
	missing := Mount{Mountpoint: filepath.Join(t.TempDir(), "absent")}
	if fillUsage(&missing) {
		t.Fatal("missing dir reported accessible")
	}
}

func TestFindMount(t *testing.T) {
	storage := Storage{Mounts: []Mount{
		{Mountpoint: "/"},
		{Mountpoint: "/mnt/data"},
		{Mountpoint: "/mnt/data/media"},
	}}
	got, ok := storage.FindMount("/mnt/data/media/movies/f.mkv")
	if !ok || got.Mountpoint != "/mnt/data/media" {
		t.Fatalf("got %+v, %v; want longest prefix", got, ok)
	}
	got, ok = storage.FindMount("/etc/hostname")
	if !ok || got.Mountpoint != "/" {
		t.Fatalf("got %+v, %v; want /", got, ok)
	}
}

func TestDiscoverStorageLive(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only discovery")
	}
	got, err := DiscoverStorage()
	if err != nil {
		t.Fatalf("DiscoverStorage: %v", err)
	}
	if len(got.Mounts) == 0 {
		t.Fatal("no mounts discovered")
	}
	root, ok := got.FindMount("/")
	if !ok || !root.Accessible || root.TotalBytes == 0 {
		t.Fatalf("root = %+v, %v", root, ok)
	}
	access := 0
	for _, mount := range got.Mounts {
		if mount.Accessible {
			access++
		}
	}
	t.Logf("mounts=%d accessible=%d root-free=%d", len(got.Mounts), access, root.FreeBytes)
}
