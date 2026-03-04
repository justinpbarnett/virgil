//go:build !darwin

package voice

import "fmt"

// HotkeyManager is a stub for non-macOS platforms.
type HotkeyManager struct {
	events chan HotkeyEvent
}

// NewHotkeyManager returns an error on non-macOS platforms.
func NewHotkeyManager(keys []string) (*HotkeyManager, error) {
	return nil, fmt.Errorf("global hotkeys not supported on this platform")
}

// Events returns the event channel (always empty on non-macOS).
func (h *HotkeyManager) Events() <-chan HotkeyEvent {
	if h == nil {
		return make(chan HotkeyEvent)
	}
	return h.events
}

// Close is a no-op on non-macOS platforms.
func (h *HotkeyManager) Close() {}
