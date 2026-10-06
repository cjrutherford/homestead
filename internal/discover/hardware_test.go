package discover

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

const cpuFlagsFixture = `processor	: 0
flags		: fpu vme aes avx avx2
processor	: 1
flags		: fpu vme aes avx2 vmx
`

func TestCPUFlagsUnion(t *testing.T) {
	got := cpuFlags([]byte(cpuFlagsFixture))
	want := []string{"aes", "avx", "avx2", "fpu", "vme", "vmx"}
	if len(got) != len(want) {
		t.Fatalf("flags = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("flags = %q, want sorted %q", got, want)
		}
	}
	hw := Hardware{CPUFlags: got}
	if !hw.HasFlag("aes") || hw.HasFlag("nope") {
		t.Fatal("HasFlag mismatch")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestReadDMIToleratesAbsence(t *testing.T) {
	vendor, model := readDMI(filepath.Join(t.TempDir(), "missing"))
	if vendor != "" || model != "" {
		t.Fatalf("got %q %q, want empties", vendor, model)
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sys_vendor"), "QEMU\n")
	writeFile(t, filepath.Join(dir, "product_name"), "Standard PC\n")
	vendor, model = readDMI(dir)
	if vendor != "QEMU" || model != "Standard PC" {
		t.Fatalf("got %q %q", vendor, model)
	}
}

func TestDiscoverGPUsFixture(t *testing.T) {
	root := t.TempDir()
	dev := filepath.Join(root, "dev")
	sys := filepath.Join(root, "sys")
	for _, node := range []string{"renderD128", "renderD129", "card0", "card1", "nvidia0", "nvidiactl", "sda", "kvm"} {
		writeFile(t, filepath.Join(dev, "dri", node), "")
	}
	// nvidia* must match at dev top level too; move them up.
	for _, node := range []string{"nvidia0", "nvidiactl"} {
		writeFile(t, filepath.Join(dev, node), "")
	}
	vga := filepath.Join(sys, "bus", "pci", "devices", "0000:00:02.0")
	writeFile(t, filepath.Join(vga, "class"), "0x030000\n")
	writeFile(t, filepath.Join(vga, "vendor"), "0x8086\n")
	writeFile(t, filepath.Join(vga, "device"), "0x5912\n")
	if err := os.MkdirAll(filepath.Join(root, "drivers", "i915"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(
		filepath.Join(root, "drivers", "i915"),
		filepath.Join(vga, "driver"),
	); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	nic := filepath.Join(sys, "bus", "pci", "devices", "0000:00:1f.6")
	writeFile(t, filepath.Join(nic, "class"), "0x020000\n")

	gpus := discoverGPUs(sys, dev)
	byKind := map[string]int{}
	for _, gpu := range gpus {
		byKind[gpu.Kind]++
	}
	if byKind[GPUKindDRMRender] != 2 || byKind[GPUKindDRMCard] != 2 {
		t.Fatalf("drm = %+v", byKind)
	}
	if byKind[GPUKindNVIDIA] != 2 {
		t.Fatalf("nvidia = %+v (want nvidia0 and nvidiactl)", byKind)
	}
	if byKind[GPUKindPCIVGA] != 1 || byKind[GPUKindPCI3D] != 0 {
		t.Fatalf("pci = %+v", byKind)
	}
	for _, gpu := range gpus {
		if gpu.Kind == GPUKindPCIVGA {
			if gpu.VendorID != "0x8086" || gpu.DeviceID != "0x5912" || gpu.Driver != "i915" {
				t.Fatalf("vga = %+v", gpu)
			}
		}
		if gpu.Device == "" {
			t.Fatalf("empty device in %+v", gpu)
		}
	}
}

func TestDiscoverGPUsMissingRoots(t *testing.T) {
	if got := discoverGPUs(
		filepath.Join(t.TempDir(), "no-sys"),
		filepath.Join(t.TempDir(), "no-dev"),
	); len(got) != 0 {
		t.Fatalf("got %v, want none", got)
	}
}

func TestDiscoverHardwareLive(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only discovery")
	}
	got, err := DiscoverHardware()
	if err != nil {
		t.Fatalf("DiscoverHardware: %v", err)
	}
	if len(got.CPUFlags) == 0 {
		t.Fatal("no CPU flags discovered")
	}
	t.Logf("machine=%q %q cpus-flags=%d gpus=%d", got.MachineVendor, got.MachineModel, len(got.CPUFlags), len(got.GPUs))
}
