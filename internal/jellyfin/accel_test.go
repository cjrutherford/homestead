package jellyfin

import (
	"testing"

	"homestead/internal/discover"
)

func pciGPU(vendor, driver string) discover.GPU {
	return discover.GPU{Kind: discover.GPUKindPCIVGA, Device: "0000:00:02.0", VendorID: vendor, Driver: driver}
}

func TestDetectAcceleration(t *testing.T) {
	intel := discover.Hardware{GPUs: []discover.GPU{
		pciGPU("0x8086", "i915"),
		{Kind: discover.GPUKindDRMRender, Device: "/dev/dri/renderD128"},
		{Kind: discover.GPUKindDRMCard, Device: "/dev/dri/card0"},
	}}
	got := DetectAcceleration(intel)
	if got.Kind != AccelQSV || len(got.Devices) != 2 || got.Runtime != "" {
		t.Fatalf("intel = %+v", got)
	}
	amd := discover.Hardware{GPUs: []discover.GPU{
		pciGPU("0x1002", "amdgpu"),
		{Kind: discover.GPUKindDRMRender, Device: "/dev/dri/renderD128"},
	}}
	got = DetectAcceleration(amd)
	if got.Kind != AccelVAAPI || len(got.Devices) != 1 {
		t.Fatalf("amd = %+v", got)
	}
	nvidia := discover.Hardware{GPUs: []discover.GPU{
		{Kind: discover.GPUKindNVIDIA, Device: "/dev/nvidia0"},
		{Kind: discover.GPUKindNVIDIA, Device: "/dev/nvidia-uvm"},
		pciGPU("0x10de", "nvidia"),
	}}
	got = DetectAcceleration(nvidia)
	if got.Kind != AccelNVENC || len(got.Devices) != 2 || got.Runtime != "nvidia" ||
		got.Env["NVIDIA_VISIBLE_DEVICES"] != "all" {
		t.Fatalf("nvidia = %+v", got)
	}
	// Discrete NVIDIA wins over integrated Intel.
	mixed := discover.Hardware{GPUs: append(append([]discover.GPU{}, intel.GPUs...), nvidia.GPUs...)}
	if got := DetectAcceleration(mixed); got.Kind != AccelNVENC {
		t.Fatalf("mixed = %+v", got)
	}
	// Half stacks fall through: vendor without nodes, nodes without vendor.
	for _, hardware := range []discover.Hardware{
		{GPUs: []discover.GPU{pciGPU("0x8086", "i915")}},
		{GPUs: []discover.GPU{{Kind: discover.GPUKindDRMRender, Device: "/dev/dri/renderD128"}}},
		{},
	} {
		if got := DetectAcceleration(hardware); got.Kind != AccelNone || len(got.Devices) != 0 {
			t.Fatalf("half = %+v", got)
		}
	}
	// Vendor IDs compare case-insensitively without prefix fuss.
	upper := discover.Hardware{GPUs: []discover.GPU{
		{Kind: discover.GPUKindPCIVGA, VendorID: "0X8086"},
		{Kind: discover.GPUKindDRMRender, Device: "/dev/dri/renderD128"},
	}}
	if got := DetectAcceleration(upper); got.Kind != AccelQSV {
		t.Fatalf("upper = %+v", got)
	}
}
