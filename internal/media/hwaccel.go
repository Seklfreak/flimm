package media

import (
	"fmt"
	"os"
	"strings"
)

// Hardware-accelerated transcoding.
//
// The HLS variant is the expensive rendition: software AV1/VP9 decode feeding
// an x264 encode is CPU-bound and runs near realtime per core, so a 4K hour
// occupies several cores for tens of minutes. An Intel iGPU does both the
// decode and the H.264 encode in fixed-function silicon; through VAAPI the
// same hour finishes in minutes and leaves the cores for everything else.
//
// It is best-effort by design, in two places. Here: the render node may not
// exist, or may not be openable by the container's uid, in which case
// `auto` quietly stays on the CPU. And at transcode time: the GPU cannot
// decode everything an archive holds (10-bit AV1 is the usual miss), so a
// failed hardware attempt falls back to the software encoder rather than
// failing the request. See HLS.

// DefaultVAAPIDevice is the DRM render node a single-GPU Linux box exposes.
// It is what a container gets when one /dev/dri device is passed in.
const DefaultVAAPIDevice = "/dev/dri/renderD128"

// HWAccelMode is the MEDIA_HWACCEL setting: which of the two paths a transcode
// starts on.
type HWAccelMode string

const (
	// HWAccelAuto uses VAAPI when the render node is there and openable, and
	// the CPU otherwise. It is the default because it is right on both a box
	// with a GPU and one without, with no configuration either way.
	HWAccelAuto HWAccelMode = "auto"
	// HWAccelVAAPI asks for VAAPI whether or not the node probes clean — for
	// the case where the device appears after start-up. Transcodes still fall
	// back to software on failure; this only skips the start-up check.
	HWAccelVAAPI HWAccelMode = "vaapi"
	// HWAccelOff never touches the GPU.
	HWAccelOff HWAccelMode = "off"
)

// HWAccel is the resolved decision, made once at start-up and used by every
// transcode. The zero value is software-only, which is what an embedder that
// sets nothing should get.
type HWAccel struct {
	// VAAPI is whether transcodes should try the GPU first.
	VAAPI bool
	// Device is the render node they should try, whether or not VAAPI is on —
	// so a log line can name the path that was looked at.
	Device string
}

// ParseHWAccelMode reads the MEDIA_HWACCEL value. Anything unrecognised (an
// empty value included) is `auto`: a typo must not silently turn a GPU box
// into a CPU one, and `auto` is safe either way.
func ParseHWAccelMode(s string) HWAccelMode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "off", "none", "false", "cpu", "software":
		return HWAccelOff
	case "vaapi":
		return HWAccelVAAPI
	default:
		return HWAccelAuto
	}
}

// ResolveHWAccel decides whether transcodes use the GPU, and returns a reason
// fit for a single start-up log line. It is deliberately the only place that
// touches the device: probing per job would cost a syscall per transcode and
// tell nobody anything.
func ResolveHWAccel(mode HWAccelMode, device string) (HWAccel, string) {
	if strings.TrimSpace(device) == "" {
		device = DefaultVAAPIDevice
	}
	switch mode {
	case HWAccelOff:
		return HWAccel{Device: device}, "disabled by MEDIA_HWACCEL"
	case HWAccelVAAPI:
		if err := probeRenderNode(device); err != nil {
			// Asked for explicitly, so it is honoured — but say out loud that
			// the node does not look usable, because every transcode is then
			// going to pay for a failed attempt before falling back.
			return HWAccel{VAAPI: true, Device: device},
				fmt.Sprintf("requested explicitly, but %s is not usable right now (%v); transcodes will try it and fall back to the CPU", device, err)
		}
		return HWAccel{VAAPI: true, Device: device}, "requested explicitly"
	default:
		if err := probeRenderNode(device); err != nil {
			return HWAccel{Device: device}, fmt.Sprintf("no usable render node (%v)", err)
		}
		return HWAccel{VAAPI: true, Device: device}, "render node is usable"
	}
}

// probeRenderNode reports whether the node is there and this process can open
// it. Read-write is what VAAPI itself needs, and the permission bit is the
// interesting half: the container runs as an unprivileged uid, so a node it is
// not in the group for exists and is still useless.
func probeRenderNode(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0) //nolint:gosec // operator-supplied device path
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.IsDir() {
		return fmt.Errorf("open %s: is a directory", path)
	}
	return nil
}
