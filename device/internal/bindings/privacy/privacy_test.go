package privacy

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T, state, timer string) *Device {
	t.Helper()
	d := &Device{Path: t.TempDir()}
	for name, value := range map[string]string{"privacy_state": state, "privacy_timer_on": timer, "privacy_trigger": "0"} {
		if err := os.WriteFile(filepath.Join(d.Path, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func TestStateFollowsKernelInsteadOfToggling(t *testing.T) {
	d := fixture(t, "0\n", "0\n")
	for _, state := range []string{"0", "1", "1", "0", "0"} {
		if err := os.WriteFile(filepath.Join(d.Path, "privacy_state"), []byte(state), 0600); err != nil {
			t.Fatal(err)
		}
		got, err := d.State()
		if err != nil || got != (state == "1") {
			t.Fatalf("state %s: muted=%v err=%v", state, got, err)
		}
	}
}

func TestPendingKernelTransitionDoesNotAuthorizeUnmute(t *testing.T) {
	d := fixture(t, "0", "1")
	if _, err := d.State(); !errors.Is(err, ErrPending) {
		t.Fatalf("pending transition: %v", err)
	}
}

func TestInvalidOrMissingStateDoesNotAuthorizeUnmute(t *testing.T) {
	for _, value := range []string{"", "-1", "2", "unmuted"} {
		d := fixture(t, value, "0")
		if _, err := d.State(); err == nil {
			t.Fatalf("accepted invalid privacy state %q", value)
		}
	}
	d := fixture(t, "0", "0")
	os.Remove(filepath.Join(d.Path, "privacy_state"))
	if _, err := d.State(); err == nil {
		t.Fatal("missing kernel state treated as unmuted")
	}
}

func TestRestoreMuteOnlyRequestsMuteAndRequiresReadback(t *testing.T) {
	d := fixture(t, "0", "0")
	if err := d.RequestMute(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(d.Path, "privacy_trigger"))
	if err != nil || string(b) != "1" {
		t.Fatalf("mute request: %q %v", b, err)
	}
	// A successful write is not proof the hardware has changed.
	if muted, err := d.State(); err != nil || muted {
		t.Fatalf("write confused with readback: %v %v", muted, err)
	}
}
