package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homestead/internal/discover"
)

func TestOverallSeverity(t *testing.T) {
	if got := overall([]Check{{Status: StatusOK}}); got != StatusOK {
		t.Fatalf("overall = %q", got)
	}
	if got := overall([]Check{{Status: StatusOK}, {Status: StatusWarn}}); got != StatusWarn {
		t.Fatalf("overall = %q", got)
	}
	if got := overall([]Check{{Status: StatusWarn}, {Status: StatusFail}, {Status: StatusOK}}); got != StatusFail {
		t.Fatalf("overall = %q", got)
	}
}

func TestDistroGate(t *testing.T) {
	ubuntu := discover.OS{DistroID: "ubuntu", DistroName: "Ubuntu", DistroVersion: "24.04"}
	if got := checkDistro(ubuntu); got.Status != StatusOK {
		t.Fatalf("ubuntu = %+v", got)
	}
	debian := discover.OS{DistroID: "debian", DistroName: "Debian GNU/Linux", DistroVersion: "12"}
	if got := checkDistro(debian); got.Status != StatusOK {
		t.Fatalf("debian = %+v", got)
	}
	other := discover.OS{DistroID: "arch", DistroName: "Arch", DistroVersion: "rolling"}
	if got := checkDistro(other); got.Status != StatusFail {
		t.Fatalf("arch = %+v, want fail", got)
	}
}

func TestMemoryThresholds(t *testing.T) {
	for _, tc := range []struct {
		bytes  int64
		status string
	}{
		{512 << 20, StatusFail},
		{FailMemoryBytes - 1, StatusFail},
		{FailMemoryBytes, StatusWarn},
		{WarnMemoryBytes - 1, StatusWarn},
		{WarnMemoryBytes, StatusOK},
		{16 << 30, StatusOK},
	} {
		if got := checkMemory(discover.OS{MemoryBytes: uint64(tc.bytes)}); got.Status != tc.status {
			t.Fatalf("%d bytes = %q, want %q", tc.bytes, got.Status, tc.status)
		}
	}
}

func TestCPUThreshold(t *testing.T) {
	hw := discover.Hardware{}
	if got := checkCPU(discover.OS{CPUCount: 1, CPUModel: "x"}, hw); got.Status != StatusWarn {
		t.Fatalf("1 core = %+v, want warn", got)
	}
	if got := checkCPU(discover.OS{CPUCount: 8, CPUModel: "x"}, hw); got.Status != StatusOK {
		t.Fatalf("8 cores = %+v, want ok", got)
	}
}

func TestGatewayAndResolvers(t *testing.T) {
	if got := checkGateway(discover.Network{}); got.Status != StatusWarn {
		t.Fatalf("no gateway = %+v, want warn", got)
	}
	net := discover.Network{DefaultGateway: "192.168.1.1", DefaultInterface: "eth0"}
	if got := checkGateway(net); got.Status != StatusOK {
		t.Fatalf("gateway = %+v, want ok", got)
	}
	if got := checkResolvers(discover.Network{}); got.Status != StatusWarn {
		t.Fatalf("no resolvers = %+v, want warn", got)
	}
	net.Resolvers = []string{"192.168.1.1"}
	if got := checkResolvers(net); got.Status != StatusOK {
		t.Fatalf("resolvers = %+v, want ok", got)
	}
}

func TestRootDiskThresholds(t *testing.T) {
	at := func(avail int64) discover.Storage {
		return discover.Storage{Mounts: []discover.Mount{
			{Mountpoint: "/", Accessible: true, AvailBytes: uint64(avail)},
		}}
	}
	if got := checkRootDisk(at(1 << 30)); got.Status != StatusFail {
		t.Fatalf("1GiB = %+v, want fail", got)
	}
	if got := checkRootDisk(at(7 << 30)); got.Status != StatusWarn {
		t.Fatalf("7GiB = %+v, want warn", got)
	}
	if got := checkRootDisk(at(50 << 30)); got.Status != StatusOK {
		t.Fatalf("50GiB = %+v, want ok", got)
	}
	if got := checkRootDisk(discover.Storage{}); got.Status != StatusFail {
		t.Fatalf("no mounts = %+v, want fail", got)
	}
}

func TestStaleMounts(t *testing.T) {
	full := discover.Storage{Mounts: []discover.Mount{
		{Mountpoint: "/", FSType: "ext4", Accessible: true},
		{Mountpoint: "/mnt/old", FSType: "nfs", Accessible: false},
	}}
	if got := checkStaleMounts(full); got.Status != StatusWarn {
		t.Fatalf("stale = %+v, want warn", got)
	}
	healthy := discover.Storage{Mounts: []discover.Mount{
		{Mountpoint: "/", FSType: "ext4", Accessible: true},
	}}
	if got := checkStaleMounts(healthy); got.Status != StatusOK {
		t.Fatalf("healthy = %+v, want ok", got)
	}
}
func TestContainerPlumbingIgnored(t *testing.T) {
	// Docker netns mounts fail to stat from outside their namespace on
	// healthy hosts; flagging them would cry wolf on every appliance
	// once Docker arrives (S070+).
	noisy := discover.Storage{Mounts: []discover.Mount{
		{Mountpoint: "/", FSType: "ext4", Accessible: true},
		{Mountpoint: "/run/docker/netns/abc", FSType: "nsfs", Accessible: false},
		{Mountpoint: "/run/snapd/ns", FSType: "tmpfs", Accessible: false},
	}}
	if got := checkStaleMounts(noisy); got.Status != StatusOK {
		t.Fatalf("plumbing = %+v, want ok", got)
	}

	var many []discover.Mount
	many = append(many, discover.Mount{Mountpoint: "/", FSType: "ext4", Accessible: true})
	for i := 0; i < 11; i++ {
		many = append(many, discover.Mount{
			Mountpoint: "/mnt/stale",
			FSType:     "nfs",
			Accessible: false,
		})
	}
	got := checkStaleMounts(discover.Storage{Mounts: many})
	if got.Status != StatusWarn || !strings.Contains(got.Detail, "and 1 more") {
		t.Fatalf("capped = %+v, want truncation", got)
	}
}

func TestLiveReportStructure(t *testing.T) {
	report := Run()
	if len(report.Checks) == 0 {
		t.Fatal("empty report")
	}
	seen := map[string]bool{}
	for _, check := range report.Checks {
		if check.Name == "" || check.Detail == "" {
			t.Fatalf("thin check: %+v", check)
		}
		if check.Status != StatusOK && check.Status != StatusWarn && check.Status != StatusFail {
			t.Fatalf("bad status %+v", check)
		}
		if seen[check.Name] {
			t.Fatalf("duplicate check %q", check.Name)
		}
		seen[check.Name] = true
		if report.Overall == StatusOK && check.Status != StatusOK {
			t.Fatalf("overall ok with %+v", check)
		}
	}
	t.Logf("overall=%s checks=%d", report.Overall, len(report.Checks))
}

func TestCheckDockerPools(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) string {
		p := filepath.Join(dir, "daemon.json")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if c := checkDockerPools(filepath.Join(dir, "missing.json")); c.Status != StatusWarn {
		t.Errorf("missing file: %+v", c)
	}
	if c := checkDockerPools(write(`{"default-address-pools":[{"base":"10.200.0.0/16","size":24}]}`)); c.Status != StatusOK {
		t.Errorf("roomy pool: %+v", c)
	}
	if c := checkDockerPools(write(`{"default-address-pools":[{"base":"10.200.0.0/24","size":24}]}`)); c.Status != StatusWarn {
		t.Errorf("small pool: %+v", c)
	}
}
