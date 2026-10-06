package discover

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// GPU kinds reported by hardware discovery.
const (
	// GPUKindDRMRender is a /dev/dri/renderD* node (transcode/offload target).
	GPUKindDRMRender = "drm-render"
	// GPUKindDRMCard is a /dev/dri/card* node (display/compute).
	GPUKindDRMCard = "drm-card"
	// GPUKindNVIDIA is a /dev/nvidia* node (proprietary stack).
	GPUKindNVIDIA = "nvidia"
	// GPUKindPCIVGA is a PCI VGA controller (class 0x030000).
	GPUKindPCIVGA = "pci-vga"
	// GPUKindPCI3D is a PCI 3D controller (class 0x030200).
	GPUKindPCI3D = "pci-3d"
)

// Production roots, kept as parameters below for fixture testing.
const (
	sysRoot  = "/sys"
	devRoot  = "/dev"
	driDir   = "dri"
	pciClass = "bus/pci/devices"
	dmiDir   = "devices/virtual/dmi/id"
)

var (
	renderNode = regexp.MustCompile(`^renderD[0-9]+$`)
	cardNode   = regexp.MustCompile(`^card[0-9]+$`)
	nvidiaNode = regexp.MustCompile(`^nvidia([0-9]+|ctl)$`)
)

// GPU is one relevant graphics/compute device.
type GPU struct {
	Kind     string
	Device   string
	VendorID string
	DeviceID string
	Driver   string
}

// Hardware is the S041 fact set: machine identity, CPU capabilities, and
// relevant GPU devices. Display/transcode decisions belong to later slices
// (notably S162); this only reports what exists.
type Hardware struct {
	MachineVendor string
	MachineModel  string
	CPUFlags      []string
	GPUs          []GPU
}

// DiscoverHardware collects the hardware fact set from the local host.
// Missing optional sources (containers often lack DMI or /dev/dri) yield
// empty values, never errors; only an unreadable cpuinfo fails.
func DiscoverHardware() (Hardware, error) {
	cpuinfo, err := os.ReadFile(cpuinfoPath)
	if err != nil {
		return Hardware{}, fmt.Errorf("discover: read cpuinfo: %w", err)
	}
	vendor, model := readDMI(filepath.Join(sysRoot, dmiDir))
	return Hardware{
		MachineVendor: vendor,
		MachineModel:  model,
		CPUFlags:      cpuFlags(cpuinfo),
		GPUs:          discoverGPUs(sysRoot, devRoot),
	}, nil
}

// readDMI returns sys_vendor and product_name, tolerating absence.
func readDMI(dir string) (vendor, model string) {
	vendorBytes, err := os.ReadFile(filepath.Join(dir, "sys_vendor"))
	if err == nil {
		vendor = strings.TrimSpace(string(vendorBytes))
	}
	modelBytes, err := os.ReadFile(filepath.Join(dir, "product_name"))
	if err == nil {
		model = strings.TrimSpace(string(modelBytes))
	}
	return vendor, model
}

// cpuFlags returns the sorted union of all cpuinfo flags lines.
func cpuFlags(cpuinfo []byte) []string {
	set := map[string]bool{}
	for _, line := range strings.Split(string(cpuinfo), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(key) != "flags" {
			continue
		}
		for _, flag := range strings.Fields(value) {
			set[flag] = true
		}
	}
	flags := make([]string, 0, len(set))
	for flag := range set {
		flags = append(flags, flag)
	}
	sort.Strings(flags)
	return flags
}

// HasFlag reports whether the CPU exposes a capability flag.
func (h Hardware) HasFlag(flag string) bool {
	for _, f := range h.CPUFlags {
		if f == flag {
			return true
		}
	}
	return false
}

// discoverGPUs enumerates DRM nodes, NVIDIA nodes, and PCI display
// controllers. All sources are optional; failures yield fewer devices.
func discoverGPUs(sysFS, devFS string) []GPU {
	var gpus []GPU
	gpus = append(gpus, driNodes(filepath.Join(devFS, driDir))...)
	gpus = append(gpus, nvidiaNodes(devFS)...)
	gpus = append(gpus, pciGPUs(filepath.Join(sysFS, pciClass))...)
	return gpus
}

func driNodes(dir string) []GPU {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var gpus []GPU
	for _, entry := range entries {
		name := entry.Name()
		switch {
		case renderNode.MatchString(name):
			gpus = append(gpus, GPU{Kind: GPUKindDRMRender, Device: filepath.Join(dir, name)})
		case cardNode.MatchString(name):
			gpus = append(gpus, GPU{Kind: GPUKindDRMCard, Device: filepath.Join(dir, name)})
		}
	}
	return gpus
}

func nvidiaNodes(devFS string) []GPU {
	entries, err := os.ReadDir(devFS)
	if err != nil {
		return nil
	}
	var gpus []GPU
	for _, entry := range entries {
		if name := entry.Name(); nvidiaNode.MatchString(name) {
			gpus = append(gpus, GPU{Kind: GPUKindNVIDIA, Device: filepath.Join(devFS, name)})
		}
	}
	return gpus
}

func pciGPUs(devicesDir string) []GPU {
	entries, err := os.ReadDir(devicesDir)
	if err != nil {
		return nil
	}
	var gpus []GPU
	for _, entry := range entries {
		dir := filepath.Join(devicesDir, entry.Name())
		class, err := readTrimmed(filepath.Join(dir, "class"))
		if err != nil {
			continue
		}
		var kind string
		switch class {
		case "0x030000":
			kind = GPUKindPCIVGA
		case "0x030200":
			kind = GPUKindPCI3D
		default:
			continue
		}
		vendor, _ := readTrimmed(filepath.Join(dir, "vendor"))
		device, _ := readTrimmed(filepath.Join(dir, "device"))
		driver := ""
		if target, err := os.Readlink(filepath.Join(dir, "driver")); err == nil {
			driver = filepath.Base(target)
		}
		gpus = append(gpus, GPU{
			Kind:     kind,
			Device:   entry.Name(),
			VendorID: vendor,
			DeviceID: device,
			Driver:   driver,
		})
	}
	return gpus
}
