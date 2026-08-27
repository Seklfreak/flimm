package media

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseHWAccelMode(t *testing.T) {
	for in, want := range map[string]HWAccelMode{
		"":         HWAccelAuto,
		"auto":     HWAccelAuto,
		"  AUTO  ": HWAccelAuto,
		"vaapi":    HWAccelVAAPI,
		"VAAPI":    HWAccelVAAPI,
		"off":      HWAccelOff,
		"none":     HWAccelOff,
		"false":    HWAccelOff,
		"software": HWAccelOff,
		"cpu":      HWAccelOff,
		// A typo must not quietly disable the GPU; auto is safe either way.
		"vappi": HWAccelAuto,
	} {
		if got := ParseHWAccelMode(in); got != want {
			t.Errorf("ParseHWAccelMode(%q) = %q, want %q", in, got, want)
		}
	}
}

// auto is the whole point of the setting: the same configuration has to be
// right on a box with a render node and on one without.
func TestResolveHWAccelAuto(t *testing.T) {
	// A plain file stands in for the render node: what the probe actually
	// asks is "is it there and can this process open it read-write", and a
	// temp file answers yes to exactly that.
	device := filepath.Join(t.TempDir(), "renderD128")
	if err := os.WriteFile(device, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	hw, reason := ResolveHWAccel(HWAccelAuto, device)
	if !hw.VAAPI {
		t.Errorf("auto with a usable device should enable vaapi (reason: %s)", reason)
	}
	if hw.Device != device {
		t.Errorf("device = %q, want %q", hw.Device, device)
	}
	if reason == "" {
		t.Error("the decision must come with a reason to log")
	}

	missing := filepath.Join(t.TempDir(), "does-not-exist")
	hw, reason = ResolveHWAccel(HWAccelAuto, missing)
	if hw.VAAPI {
		t.Error("auto with no device must stay on the CPU")
	}
	// The path is still reported, so the log line can name what was looked at.
	if hw.Device != missing {
		t.Errorf("device = %q, want %q", hw.Device, missing)
	}
	if reason == "" {
		t.Error("a CPU decision must say why")
	}

	// A directory is not a render node, however openable it looks.
	hw, _ = ResolveHWAccel(HWAccelAuto, t.TempDir())
	if hw.VAAPI {
		t.Error("a directory must not pass as a render node")
	}
}

// An empty device falls back to the conventional single-GPU render node, so
// MEDIA_VAAPI_DEVICE only has to be set on a box with more than one.
func TestResolveHWAccelDefaultsTheDevice(t *testing.T) {
	hw, _ := ResolveHWAccel(HWAccelOff, "")
	if hw.Device != DefaultVAAPIDevice {
		t.Errorf("device = %q, want %q", hw.Device, DefaultVAAPIDevice)
	}
}

func TestResolveHWAccelOff(t *testing.T) {
	device := filepath.Join(t.TempDir(), "renderD128")
	if err := os.WriteFile(device, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if hw, _ := ResolveHWAccel(HWAccelOff, device); hw.VAAPI {
		t.Error("off must not use the GPU even with a perfectly good device")
	}
}

// Asked for explicitly, VAAPI is honoured even when the node does not probe
// clean — the device may appear after start-up, and a failed attempt costs a
// fallback, not a failed request.
func TestResolveHWAccelExplicit(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	hw, reason := ResolveHWAccel(HWAccelVAAPI, missing)
	if !hw.VAAPI {
		t.Errorf("explicit vaapi should be honoured, got reason %q", reason)
	}
	if reason == "" {
		t.Error("an unusable-but-requested device must say so")
	}
}
