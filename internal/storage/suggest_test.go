package storage

import (
	"reflect"
	"testing"

	"homestead/internal/discover"
)

const gib = 1 << 30

func disk(device, mountpoint, label string, size uint64) discover.Mount {
	return discover.Mount{Device: device, Mountpoint: mountpoint, Label: label, FSType: "ext4", Accessible: true, TotalBytes: size}
}

// vm2 is the KVM test VM's layout: the root disk with the daemon's
// sandbox bind mounts, three labelled data disks, and kernel noise.
func vm2() []discover.Mount {
	return []discover.Mount{
		disk("/dev/vda1", "/", "", 20*gib),
		disk("/dev/vda1", "/etc/homestead", "", 20*gib),
		disk("/dev/vda1", "/var/lib/homestead", "", 20*gib),
		disk("/dev/vda1", "/media", "", 20*gib),
		disk("/dev/vda1", "/mnt", "", 20*gib),
		disk("/dev/vdc", "/mnt/app", "app", 20*gib),
		disk("/dev/vdd", "/mnt/bulk", "bulk", 100*gib),
		disk("/dev/vde", "/mnt/backup", "backup", 50*gib),
		{Device: "proc", Mountpoint: "/proc", FSType: "proc"},
		{Device: "overlay", Mountpoint: "/var/lib/docker/overlay2/x/merged", FSType: "overlay", Accessible: true},
	}
}

func TestMountKind(t *testing.T) {
	all := vm2()
	want := map[string]string{
		"/":                                 KindSystem,
		"/etc/homestead":                    KindSystem,
		"/var/lib/homestead":                KindSystem,
		"/media":                            KindSystem,
		"/mnt":                              KindSystem,
		"/mnt/app":                          KindData,
		"/mnt/bulk":                         KindData,
		"/mnt/backup":                       KindData,
		"/proc":                             KindUnusable,
		"/var/lib/docker/overlay2/x/merged": KindUnusable,
	}
	for _, m := range all {
		if got := MountKind(m, all); got != want[m.Mountpoint] {
			t.Errorf("%s: kind %q, want %q", m.Mountpoint, got, want[m.Mountpoint])
		}
	}
}

func TestSuggestByName(t *testing.T) {
	got := Suggest(vm2(), nil)
	want := map[string][]Class{
		"/mnt/app":    {ClassApplication, ClassDatabase},
		"/mnt/bulk":   {ClassBulk},
		"/mnt/backup": {ClassBackup},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestSuggestLeavesAssignedClasses(t *testing.T) {
	got := Suggest(vm2(), map[Class]string{ClassApplication: "/mnt/app", ClassBulk: "/mnt/bulk"})
	want := map[string][]Class{
		"/mnt/app":    {ClassDatabase},
		"/mnt/backup": {ClassBackup},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestSuggestBySize(t *testing.T) {
	all := []discover.Mount{
		disk("/dev/sda1", "/", "", 100*gib),
		disk("/dev/sdb1", "/srv/one", "", 4000*gib),
		disk("/dev/sdc1", "/srv/two", "", 500*gib),
		disk("/dev/sdd1", "/srv/three", "", 2000*gib),
	}
	got := Suggest(all, nil)
	want := map[string][]Class{
		"/srv/two":   {ClassApplication, ClassDatabase},
		"/srv/one":   {ClassBulk},
		"/srv/three": {ClassBackup},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestSuggestOneDiskNeverBackup(t *testing.T) {
	all := []discover.Mount{disk("/dev/sda1", "/", "", 100*gib), disk("/dev/sdb1", "/mnt/data", "", 1000*gib)}
	want := map[string][]Class{"/mnt/data": {ClassApplication, ClassDatabase, ClassBulk}}
	if got := Suggest(all, nil); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestSuggestNothingWithoutDataDisks(t *testing.T) {
	all := []discover.Mount{disk("/dev/sda1", "/", "", 100*gib), disk("/dev/sda1", "/opt/homestead/stacks", "", 100*gib)}
	if got := Suggest(all, nil); len(got) != 0 {
		t.Fatalf("root-disk paths must not be suggested: %v", got)
	}
}
