// Package discover reports host facts for Phase 3 (S040+).
//
// Read-only by contract: it observes the host through /etc and /proc and
// never mutates anything. Linux (Debian/Ubuntu) only, matching the product
// statement — other platforms fail loudly. Surfacing these facts over the
// API/CLI belongs to later slices (S044 aggregates them into the doctor).
package discover

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// OS is the S040 fact set: distro, kernel, architecture, hostname, memory,
// and CPU. Deeper hardware (devices, accelerators) belongs to S041.
type OS struct {
	DistroID      string
	DistroName    string
	DistroVersion string
	Kernel        string
	Arch          string
	Hostname      string
	MemoryBytes   uint64
	CPUCount      int
	CPUModel      string
}

// Production source paths, kept as vars for clarity (parsers take bytes).
const (
	osReleasePath = "/etc/os-release"
	kernelPath    = "/proc/sys/kernel/osrelease"
	meminfoPath   = "/proc/meminfo"
	cpuinfoPath   = "/proc/cpuinfo"
)

// DiscoverOS collects the OS fact set from the local host.
func DiscoverOS() (OS, error) {
	if runtime.GOOS != "linux" {
		return OS{}, fmt.Errorf("discover: unsupported platform %s (Debian/Ubuntu Linux required)", runtime.GOOS)
	}
	release, err := os.ReadFile(osReleasePath)
	if err != nil {
		return OS{}, fmt.Errorf("discover: read os-release: %w", err)
	}
	id, name, version := parseOSRelease(release)
	kernel, err := readTrimmed(kernelPath)
	if err != nil {
		return OS{}, fmt.Errorf("discover: read kernel release: %w", err)
	}
	if kernel == "" {
		return OS{}, fmt.Errorf("discover: empty kernel release")
	}
	hostname, err := os.Hostname()
	if err != nil {
		return OS{}, fmt.Errorf("discover: hostname: %w", err)
	}
	meminfo, err := os.ReadFile(meminfoPath)
	if err != nil {
		return OS{}, fmt.Errorf("discover: read meminfo: %w", err)
	}
	memory, err := parseMemTotal(meminfo)
	if err != nil {
		return OS{}, err
	}
	cpuinfo, err := os.ReadFile(cpuinfoPath)
	if err != nil {
		return OS{}, fmt.Errorf("discover: read cpuinfo: %w", err)
	}
	return OS{
		DistroID:      id,
		DistroName:    name,
		DistroVersion: version,
		Kernel:        kernel,
		Arch:          runtime.GOARCH,
		Hostname:      hostname,
		MemoryBytes:   memory,
		CPUCount:      runtime.NumCPU(),
		CPUModel:      parseCPUModel(cpuinfo),
	}, nil
}

func readTrimmed(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// parseOSRelease extracts ID, NAME, and VERSION_ID from os-release content.
func parseOSRelease(data []byte) (id, name, version string) {
	values := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		values[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"`)
	}
	return values["ID"], values["NAME"], values["VERSION_ID"]
}

// parseMemTotal converts the MemTotal kB line to bytes.
func parseMemTotal(data []byte) (uint64, error) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == "MemTotal:" && fields[2] == "kB" {
			kibibytes, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				return 0, fmt.Errorf("discover: parse MemTotal: %w", err)
			}
			return kibibytes * 1024, nil
		}
	}
	return 0, fmt.Errorf("discover: MemTotal not found")
}

// parseCPUModel returns the first CPU model name, falling back through ARM
// identification lines (Hardware preferred over Processor), or "" when
// unknowable.
func parseCPUModel(data []byte) string {
	var hardware, processor string
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		switch strings.TrimSpace(key) {
		case "model name":
			return value
		case "Hardware":
			if hardware == "" {
				hardware = value
			}
		case "Processor":
			if processor == "" {
				processor = value
			}
		}
	}
	if hardware != "" {
		return hardware
	}
	return processor
}
