package jellyfin

import (
	"strings"

	"homestead/internal/discover"
)

// AccelKind selects the transcode path: vendor stacks where detected,
// software fallback otherwise. ARM SoC paths (rkmpp and friends)
// stay out: the goal names Intel, AMD, and NVIDIA, and SoC bindings
// need per-board validation no slice owns yet.
type AccelKind string

const (
	// AccelNone transcodes on CPU: always available, never fastest.
	AccelNone AccelKind = "none"
	// AccelVAAPI drives AMD GPUs through render nodes.
	AccelVAAPI AccelKind = "vaapi"
	// AccelQSV drives Intel Quick Sync through render nodes.
	AccelQSV AccelKind = "qsv"
	// AccelNVENC drives NVIDIA GPUs through the proprietary stack.
	AccelNVENC AccelKind = "nvenc"
)

// Acceleration is the detected transcode configuration: kind, host
// device paths to pass through, container runtime, and environment.
// Group additions stay out deliberately: container users vary, host
// video GIDs vary more, and wrong groups fail louder than missing
// ones — owners add their host video GID when permission errors say so.
type Acceleration struct {
	Kind    AccelKind
	Devices []string
	Runtime string
	Env     map[string]string
}

// pciVendor normalizes sysfs vendor IDs ("0x8086") for comparison.
func pciVendor(id string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(id)), "0x")
}

// DetectAcceleration maps discovered GPUs to the transcode path,
// preferring discrete NVIDIA, then Intel Quick Sync, then AMD VAAPI.
// Detection needs both sides of the stack: the vendor device and its
// usable node (render nodes for VAAPI/QSV, nvidia nodes for NVENC).
// Half-present stacks fall through to software rather than planning
// mappings the kernel cannot serve.
func DetectAcceleration(hardware discover.Hardware) Acceleration {
	var nvidia, drm []string
	intel, amd := false, false
	for _, gpu := range hardware.GPUs {
		switch gpu.Kind {
		case discover.GPUKindNVIDIA:
			nvidia = append(nvidia, gpu.Device)
		case discover.GPUKindDRMRender, discover.GPUKindDRMCard:
			if gpu.Device != "" {
				drm = append(drm, gpu.Device)
			}
		case discover.GPUKindPCIVGA, discover.GPUKindPCI3D:
			switch pciVendor(gpu.VendorID) {
			case "8086":
				intel = true
			case "1002":
				amd = true
			}
		}
	}
	switch {
	case len(nvidia) > 0:
		return Acceleration{
			Kind: AccelNVENC, Devices: nvidia, Runtime: "nvidia",
			Env: map[string]string{"NVIDIA_VISIBLE_DEVICES": "all"},
		}
	case intel && len(drm) > 0:
		return Acceleration{Kind: AccelQSV, Devices: drm}
	case amd && len(drm) > 0:
		return Acceleration{Kind: AccelVAAPI, Devices: drm}
	default:
		return Acceleration{Kind: AccelNone}
	}
}
