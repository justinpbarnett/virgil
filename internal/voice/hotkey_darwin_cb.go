//go:build darwin

package voice

/*
// Only declarations allowed in //export files
extern void goHandleHotkeyEvent(char* key, char* action);
*/
import "C"

//export goHandleHotkeyEvent
func goHandleHotkeyEvent(key *C.char, action *C.char) {
	globalHotkeyMu.Lock()
	m := globalHotkeyManager
	globalHotkeyMu.Unlock()

	if m == nil {
		return
	}

	k := C.GoString(key)
	if !m.registeredKeys[k] {
		return
	}

	event := HotkeyEvent{Key: k, Action: C.GoString(action)}
	select {
	case m.events <- event:
	default:
	}
}
