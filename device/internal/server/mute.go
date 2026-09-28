package server

import (
	"errors"
	"log"
	"sync"
	"time"

	internalLed "github.com/wilbowes/EchoMuse/internal/bindings/led"
	"github.com/wilbowes/EchoMuse/internal/bindings/mixer"
	"github.com/wilbowes/EchoMuse/internal/bindings/privacy"
	"github.com/wilbowes/EchoMuse/pkg/led"
)

type muteController struct {
	hardware          kernelPrivacy
	transitionMu      sync.Mutex
	restoringHardware bool
	hardwareError     string
	restoreDone       chan struct{}
	restoreError      error // published by closing restoreDone
	restoreStarted    time.Time
	restoreTimedOut   bool
	mu                sync.Mutex
	muted             bool
	ledCtrl           func() led.Controller
	// dotMuted is set externally to block dot button events while muted
	onMuteChange func(muted bool)
	// persist, when set, is called after every Toggle() so the mute state
	// survives reboots and OTA restarts (state.json). Separate from
	// onMuteChange: that one is the controller-notification hook wired by
	// cmd, this one is internal.
	persist func()
}

type kernelPrivacy interface {
	State() (bool, error)
	RequestMute() error
}

func newMuteController(ledGetter func() led.Controller, onMuteChange func(muted bool)) *muteController {
	m := &muteController{
		ledCtrl:      ledGetter,
		onMuteChange: onMuteChange,
	}
	if privacy.Present() {
		m.hardware = &privacy.Device{Path: privacy.Path}
	}
	return m
}

// SetOnMuteChange wires a callback invoked when mute state changes.
// B7 fix (2026-07-05 review): previously Server.SetMuteChangeCallback
// reached directly into m.mu/m.onMuteChange from outside this struct.
// Encapsulating the lock here keeps muteController responsible for its
// own synchronisation, matching every other muteController method.
func (m *muteController) SetOnMuteChange(cb func(muted bool)) {
	m.mu.Lock()
	m.onMuteChange = cb
	m.mu.Unlock()
}

func (m *muteController) IsMuted() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.muted
}

func (m *muteController) Toggle() {
	if m.hardware != nil {
		// The kernel already handles this physical press (including delayed
		// work). Its poller will reconcile; a second independent toggle is
		// exactly what inverted Radar's button and application state.
		return
	}
	m.transitionMu.Lock()
	defer m.transitionMu.Unlock()
	m.setMuted(!m.IsMuted())
}

// setMuted runs with transitionMu held, serialising hardware, LEDs, persisted
// state and callbacks rather than only protecting the in-memory flag.
func (m *muteController) setMuted(muted bool) {
	m.mu.Lock()
	if m.muted == muted {
		m.mu.Unlock()
		return
	}
	m.muted = muted
	// Copy under the lock — SetOnMuteChange writes this field under mu from
	// the main goroutine, and button events can fire before that wiring
	// completes (SubscribeToButton starts the evdev goroutines first).
	cb := m.onMuteChange
	persist := m.persist
	m.mu.Unlock()

	if muted {
		m.applyMute()
	} else {
		m.applyUnmute()
	}
	if persist != nil {
		persist()
	}

	if cb != nil {
		cb(muted)
	}
}

func (m *muteController) syncHardware() {
	m.transitionMu.Lock()
	defer m.transitionMu.Unlock()
	if m.restoreDone != nil {
		select {
		case <-m.restoreDone:
			if m.restoreError != nil {
				log.Printf("Mute: hardware restore failed; retaining software mute: %v", m.restoreError)
			}
			m.restoreDone = nil
		default:
			if !m.restoreTimedOut && time.Since(m.restoreStarted) >= 2*time.Second {
				log.Print("Mute: hardware restore still blocked after 2s; mic remains muted, controller recovery stays available")
				m.restoreTimedOut = true
			}
			return
		}
	}
	muted, err := m.hardware.State()
	if err != nil {
		if !errors.Is(err, privacy.ErrPending) && err.Error() != m.hardwareError {
			log.Printf("Mute: privacy read failed; keeping mic muted: %v", err)
			m.hardwareError = err.Error()
		}
		m.setMuted(true)
		return
	}
	m.hardwareError = ""
	if m.restoringHardware {
		if !muted {
			return
		} // write acceptance alone must not release saved mute
		m.restoringHardware = false
	}
	m.setMuted(muted)
}

// Process-lifetime poller: kernel privacy can change after the release event
// (300ms delayed work), or while application state was being restored.
func (m *muteController) watchHardware() {
	if m.hardware == nil {
		return
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		m.syncHardware()
	}
}

// adcMuteCtls are the per-chip ADC mute controls, all four codecs
// (A: ch0/ch1 … D: ch6 + unused). C5 hardware fix (2026-07-07): only chip
// A was muted before, leaving chips B–D — including ch6, the mic wake word
// and STT actually use — physically hot; the mic stream-stop was what made
// mute effective. By name since 2026-09-17 (#546).
var adcMuteCtls = []string{
	"ADC_A Left Mute", "ADC_A Right Mute",
	"ADC_B Left Mute", "ADC_B Right Mute",
	"ADC_C Left Mute", "ADC_C Right Mute",
	"ADC_D Left Mute", "ADC_D Right Mute",
}

// setAdcMute reports every failure, not just the first per control: this is
// the hardware half of the mute, and a silent miss here is a hot microphone.
func setAdcMute(val string) {
	failed := 0
	for _, ctl := range adcMuteCtls {
		if mixer.Set(ctl, val) != nil {
			failed++
		}
	}
	if failed > 0 {
		log.Printf("Mute: %d of %d ADC mute controls failed to set %s", failed, len(adcMuteCtls), val)
	}
}

// RestoreMuted re-applies a persisted muted state at boot: flag + ADC mute
// only. The LED hardware isn't up yet when this runs (NewServer, before the
// LED-init goroutine finishes), so the red ring and button LED are painted
// by that goroutine once the controllers exist.
func (m *muteController) RestoreMuted() {
	m.transitionMu.Lock()
	defer m.transitionMu.Unlock()
	m.mu.Lock()
	m.muted = true
	m.mu.Unlock()
	log.Println("Mute: restoring persisted muted state")
	setAdcMute("1")
	if m.hardware != nil {
		m.restoringHardware = true
		if muted, err := m.hardware.State(); err == nil && muted {
			m.restoringHardware = false
		} else {
			// Some kernels wait indefinitely inside this sysfs write for a
			// hardware latch. Never hold startup or transitionMu hostage.
			// One request only; software stays closed until it finishes and
			// the poller confirms the hardware state. A pending write must
			// not outlive a software unmute and reassert privacy afterward.
			m.restoreStarted = time.Now()
			m.restoreDone = make(chan struct{})
			done := m.restoreDone
			go func() {
				m.restoreError = m.hardware.RequestMute()
				close(done)
			}()
		}
	}
}

func (m *muteController) applyMute() {
	log.Println("Mute: mic muted")
	setAdcMute("1")
	m.showMuteLEDs()
	if m.hardware == nil {
		setMuteButtonLED(true)
	}
}

func (m *muteController) applyUnmute() {
	log.Println("Mute: mic unmuted")
	setAdcMute("0")
	m.clearLEDs()
	if m.hardware == nil {
		setMuteButtonLED(false)
	}
}

// setMuteButtonLED drives the discrete red LED under the mic-off button —
// stock-Alexa parity: the button itself shows muted, not just the ring.
// GPIO-backed and independent of the ring driver, so it needs no repaint
// protection (ring repaints can't stomp it) and survives every LED-mode
// transition for free. Direct binding call, same precedent as setAdcMute's
// tinymix exec above.
func setMuteButtonLED(on bool) {
	if err := internalLed.SetMuteButtonLED(on); err != nil {
		log.Printf("Mute button LED: %v", err)
	}
}

func (m *muteController) showMuteLEDs() {
	lc := m.ledCtrl()
	if lc == nil {
		return
	}
	leds := make([]led.Led, numLEDs)
	for i := 0; i < numLEDs; i++ {
		leds[i] = led.Led{ID: i, R: 180, G: 0, B: 0} // red ring
	}
	if err := lc.SetLEDs(leds...); err != nil {
		log.Printf("Mute LED set failed: %v", err)
	}
}

func (m *muteController) clearLEDs() {
	lc := m.ledCtrl()
	if lc == nil {
		return
	}
	clearLeds(lc)
}
