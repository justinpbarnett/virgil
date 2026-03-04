package voice

// HotkeyEvent represents a key press or release from a global hotkey.
type HotkeyEvent struct {
	Key    string // e.g., "right_option", "f8"
	Action string // "down" or "up"
}
