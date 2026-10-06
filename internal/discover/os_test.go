package discover

import (
	"runtime"
	"testing"
)

const ubuntuRelease = `# Ubuntu Noble
NAME="Ubuntu"
VERSION="24.04.1 LTS (Noble Numbat)"
ID=ubuntu
ID_LIKE=debian
VERSION_ID="24.04"
`

const debianRelease = `PRETTY_NAME="Debian GNU/Linux 12 (bookworm)"
NAME="Debian GNU/Linux"
VERSION_ID="12"
VERSION="12 (bookworm)"
VERSION_CODENAME=bookworm
ID=debian
`

func TestParseOSRelease(t *testing.T) {
	id, name, version := parseOSRelease([]byte(ubuntuRelease))
	if id != "ubuntu" || name != "Ubuntu" || version != "24.04" {
		t.Fatalf("ubuntu = %q %q %q", id, name, version)
	}
	id, name, version = parseOSRelease([]byte(debianRelease))
	if id != "debian" || name != "Debian GNU/Linux" || version != "12" {
		t.Fatalf("debian = %q %q %q", id, name, version)
	}
	id, name, version = parseOSRelease([]byte("garbage\nno-equals-here\n"))
	if id != "" || name != "" || version != "" {
		t.Fatalf("garbage = %q %q %q, want empties", id, name, version)
	}
}

const meminfoFixture = `MemTotal:        4024548 kB
MemFree:          523416 kB
MemAvailable:    3105024 kB
`

func TestParseMemTotal(t *testing.T) {
	got, err := parseMemTotal([]byte(meminfoFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if want := uint64(4024548 * 1024); got != want {
		t.Fatalf("MemTotal = %d, want %d", got, want)
	}
	if _, err := parseMemTotal([]byte("MemFree: 1 kB\n")); err == nil {
		t.Fatal("missing MemTotal accepted, want error")
	}
	if _, err := parseMemTotal([]byte("MemTotal: lots kB\n")); err == nil {
		t.Fatal("garbage MemTotal accepted, want error")
	}
}

const cpuinfoX86 = `processor	: 0
vendor_id	: GenuineIntel
model name	: Intel(R) Core(TM) i7-8565U CPU @ 1.80GHz
processor	: 1
model name	: Intel(R) Core(TM) i7-8565U CPU @ 1.80GHz
`

const cpuinfoARM = `processor	: 0
Processor	: ARMv8 Processor rev 4 (v8l)
Hardware	: Raspberry Pi 4 Model B
`

func TestParseCPUModel(t *testing.T) {
	if got := parseCPUModel([]byte(cpuinfoX86)); got != "Intel(R) Core(TM) i7-8565U CPU @ 1.80GHz" {
		t.Fatalf("x86 model = %q", got)
	}
	if got := parseCPUModel([]byte(cpuinfoARM)); got != "Raspberry Pi 4 Model B" {
		t.Fatalf("arm model = %q, want Hardware fallback", got)
	}
	if got := parseCPUModel([]byte("processor : 0\n")); got != "" {
		t.Fatalf("empty model = %q, want empty", got)
	}
}

func TestDiscoverOSLive(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only discovery")
	}
	got, err := DiscoverOS()
	if err != nil {
		t.Fatalf("DiscoverOS: %v", err)
	}
	if got.Arch != runtime.GOARCH {
		t.Fatalf("Arch = %q, want %q", got.Arch, runtime.GOARCH)
	}
	for _, field := range map[string]string{
		"Kernel":   got.Kernel,
		"Hostname": got.Hostname,
	} {
		if field == "" {
			t.Fatalf("empty field in %+v", got)
		}
	}
	if got.MemoryBytes == 0 {
		t.Fatal("MemoryBytes is zero")
	}
	if got.CPUCount < 1 {
		t.Fatalf("CPUCount = %d", got.CPUCount)
	}
}
