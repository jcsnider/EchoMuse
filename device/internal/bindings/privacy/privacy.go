// Package privacy reads the kernel-owned microphone privacy state. Do not
// enumerate/read arbitrary attributes here: Radar's power_button_state getter
// dereferences an invalid GPIO and panics the kernel (verified 2026-09-28).
package privacy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const Path = "/sys/devices/soc/10010000.keypad/amz_privacy"

var ErrPending = errors.New("kernel privacy transition pending")

type Device struct{ Path string }

// Present checks existence only. Read errors must never select the legacy
// software-toggle path on a device whose kernel owns privacy.
func Present() bool {
	_, err := os.Stat(filepath.Join(Path, "privacy_state"))
	return !os.IsNotExist(err)
}

func (d *Device) read(name string) (bool, error) {
	b, err := os.ReadFile(filepath.Join(d.Path, name))
	if err != nil {
		return false, err
	}
	switch strings.TrimSpace(string(b)) {
	case "0":
		return false, nil
	case "1":
		return true, nil
	default:
		return false, fmt.Errorf("invalid %s: %q", name, b)
	}
}

// State waits for the kernel's delayed button work by rejecting pending
// samples. The caller polls; it must not invert its own previous state.
func (d *Device) State() (bool, error) {
	pending, err := d.read("privacy_timer_on")
	if err != nil {
		return false, err
	}
	if pending {
		return false, ErrPending
	}
	muted, err := d.read("privacy_state")
	if err != nil {
		return false, err
	}
	pending, err = d.read("privacy_timer_on")
	if err != nil {
		return false, err
	}
	if pending {
		return false, ErrPending
	}
	return muted, nil
}

// RequestMute preserves a saved mute across boots. There is intentionally no
// software unmute operation: only the physical button may release privacy.
func (d *Device) RequestMute() error {
	return os.WriteFile(filepath.Join(d.Path, "privacy_trigger"), []byte("1"), 0600)
}
