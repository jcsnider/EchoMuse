package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wilbowes/EchoMuse/internal/bindings/privacy"
	"github.com/wilbowes/EchoMuse/pkg/led"
)

func TestKernelMuteButtonDoesNotInvertIndependentSoftwareState(t *testing.T) {
	root := t.TempDir()
	for name, value := range map[string]string{"privacy_state": "1", "privacy_timer_on": "0"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	m := newMuteController(func() led.Controller { return nil }, nil)
	m.hardware = &privacy.Device{Path: root}
	m.muted = true // persisted true; the physical press has just made hardware true
	m.Toggle()
	if !m.IsMuted() {
		t.Fatal("physical mute became software unmute: button and ring are inverted")
	}
}

func TestKernelMuteSyncWaitsForDelayedStateAndPersistsActualState(t *testing.T) {
	root := t.TempDir()
	write := func(name, value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("privacy_state", "0")
	write("privacy_timer_on", "1")
	var reports []bool
	m := newMuteController(func() led.Controller { return nil }, func(v bool) { reports = append(reports, v) })
	m.hardware = &privacy.Device{Path: root}
	m.muted = true
	stateFile := filepath.Join(root, "state.json")
	m.persist = func() { saveDeviceState(stateFile, deviceState{Muted: m.IsMuted()}) }
	m.syncHardware()
	if !m.IsMuted() || len(reports) != 0 {
		t.Fatal("pending hardware change authorized unmute")
	}
	write("privacy_timer_on", "0")
	m.syncHardware()
	m.syncHardware()
	st, ok := loadDeviceState(stateFile)
	if m.IsMuted() || !ok || st.Muted || len(reports) != 1 || reports[0] {
		t.Fatalf("stable unmute not applied/persisted/reported once: state=%+v reports=%v", st, reports)
	}
	write("privacy_state", "1")
	m.syncHardware()
	if !m.IsMuted() || len(reports) != 2 || !reports[1] {
		t.Fatal("hardware mute not propagated")
	}
	write("privacy_state", "invalid")
	m.syncHardware()
	if !m.IsMuted() {
		t.Fatal("unreadable privacy state authorized unmute")
	}
	write("privacy_state", "0")
	m.syncHardware()
	write("privacy_state", "invalid")
	m.syncHardware()
	if !m.IsMuted() || !reports[len(reports)-1] {
		t.Fatal("loss of hardware state left microphone unmuted")
	}
}

func TestRestoreMuteCannotBeClearedUntilHardwareConfirms(t *testing.T) {
	root := t.TempDir()
	for name, value := range map[string]string{"privacy_state": "0", "privacy_timer_on": "0", "privacy_trigger": "0"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	m := newMuteController(func() led.Controller { return nil }, nil)
	m.hardware = &privacy.Device{Path: root}
	m.RestoreMuted()
	select {
	case <-m.restoreDone:
	case <-time.After(time.Second):
		t.Fatal("fixture mute write did not finish")
	}
	m.syncHardware()
	if !m.IsMuted() {
		t.Fatal("persisted mute lost before hardware restore completed")
	}
	trigger, _ := os.ReadFile(filepath.Join(root, "privacy_trigger"))
	if string(trigger) != "1" {
		t.Fatal("persisted mute did not request hardware mute")
	}
	if err := os.WriteFile(filepath.Join(root, "privacy_state"), []byte("1"), 0600); err != nil {
		t.Fatal(err)
	}
	m.syncHardware()
	if err := os.WriteFile(filepath.Join(root, "privacy_state"), []byte("0"), 0600); err != nil {
		t.Fatal(err)
	}
	m.syncHardware()
	if m.IsMuted() {
		t.Fatal("physical unmute after confirmed restore was ignored")
	}
}

type blockedPrivacy struct {
	started chan struct{}
	release chan struct{}
}

func (b *blockedPrivacy) State() (bool, error) { return false, nil }
func (b *blockedPrivacy) RequestMute() error   { close(b.started); <-b.release; return nil }

func TestBlockedKernelWriteDoesNotBlockStartupOrReconciliation(t *testing.T) {
	b := &blockedPrivacy{started: make(chan struct{}), release: make(chan struct{})}
	defer close(b.release)
	m := newMuteController(func() led.Controller { return nil }, nil)
	m.hardware = b
	returned := make(chan struct{})
	go func() { m.RestoreMuted(); close(returned) }()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("blocked kernel write stalled server startup")
	}
	<-b.started
	reconciled := make(chan struct{})
	go func() { m.syncHardware(); close(reconciled) }()
	select {
	case <-reconciled:
	case <-time.After(time.Second):
		t.Fatal("blocked write holds transition lock")
	}
	if !m.IsMuted() {
		t.Fatal("blocked hardware restore released software mute")
	}
}

func TestLegacyMuteStillTogglesWithoutKernelPrivacy(t *testing.T) {
	var reports []bool
	m := newMuteController(func() led.Controller { return nil }, func(v bool) { reports = append(reports, v) })
	m.hardware = nil
	m.Toggle()
	if !m.IsMuted() {
		t.Fatal("legacy mute stopped working")
	}
	m.Toggle()
	if m.IsMuted() || len(reports) != 2 || !reports[0] || reports[1] {
		t.Fatalf("legacy unmute/reports: %v", reports)
	}
}
