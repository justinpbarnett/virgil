//go:build darwin

package voice

/*
#cgo LDFLAGS: -framework CoreGraphics -framework ApplicationServices
#include <CoreGraphics/CoreGraphics.h>
#include <ApplicationServices/ApplicationServices.h>
#include <stdlib.h>

// Forward declaration of exported Go function (defined in hotkey_darwin_cb.go)
extern void goHandleHotkeyEvent(char* key, char* action);

// Virtual key codes
#define VIRGIL_KEY_RIGHT_OPTION 0x3D
#define VIRGIL_KEY_F8 0x64

// Device-specific right option mask in CGEventFlags
#define VIRGIL_NX_DEVICERALTKEYMASK 0x00000040

static CGEventRef virgilEventTapCallback(CGEventTapProxy proxy, CGEventType type, CGEventRef event, void *userInfo) {
    int64_t keyCode = CGEventGetIntegerValueField(event, kCGKeyboardEventKeycode);
    CGEventFlags flags = CGEventGetFlags(event);

    if (type == kCGEventFlagsChanged) {
        if (keyCode == VIRGIL_KEY_RIGHT_OPTION) {
            if (flags & VIRGIL_NX_DEVICERALTKEYMASK) {
                goHandleHotkeyEvent("right_option", "down");
            } else {
                goHandleHotkeyEvent("right_option", "up");
            }
        }
    } else if (type == kCGEventKeyDown) {
        if (keyCode == VIRGIL_KEY_F8) {
            goHandleHotkeyEvent("f8", "down");
        }
    } else if (type == kCGEventKeyUp) {
        if (keyCode == VIRGIL_KEY_F8) {
            goHandleHotkeyEvent("f8", "up");
        }
    }

    return event;
}

static CFMachPortRef virgilCreateEventTap(void) {
    CGEventMask mask = CGEventMaskBit(kCGEventFlagsChanged) |
                       CGEventMaskBit(kCGEventKeyDown) |
                       CGEventMaskBit(kCGEventKeyUp);
    return CGEventTapCreate(
        kCGSessionEventTap,
        kCGHeadInsertEventTap,
        kCGEventTapOptionListenOnly,
        mask,
        virgilEventTapCallback,
        NULL
    );
}

static void virgilRunTapLoop(CFMachPortRef tap) {
    CFRunLoopSourceRef source = CFMachPortCreateRunLoopSource(kCFAllocatorDefault, tap, 0);
    CFRunLoopAddSource(CFRunLoopGetCurrent(), source, kCFRunLoopCommonModes);
    CGEventTapEnable(tap, true);
    CFRunLoopRun();
    CFRelease(source);
}
*/
import "C"

import (
	"fmt"
	"sync"
)

var (
	globalHotkeyManager *HotkeyManager
	globalHotkeyMu      sync.Mutex
)

// HotkeyManager monitors global key events via CGEventTap.
type HotkeyManager struct {
	events     chan HotkeyEvent
	registeredKeys map[string]bool
	stopCh     chan struct{}
	tap        C.CFMachPortRef
}

// NewHotkeyManager creates a CGEventTap monitoring the given key names.
func NewHotkeyManager(keys []string) (*HotkeyManager, error) {
	tap := C.virgilCreateEventTap()
	if tap == 0 {
		return nil, fmt.Errorf("accessibility permission required: System Settings > Privacy & Security > Accessibility")
	}

	registered := make(map[string]bool, len(keys))
	for _, k := range keys {
		registered[k] = true
	}

	m := &HotkeyManager{
		events:         make(chan HotkeyEvent, 16),
		registeredKeys: registered,
		stopCh:         make(chan struct{}),
		tap:            tap,
	}

	globalHotkeyMu.Lock()
	globalHotkeyManager = m
	globalHotkeyMu.Unlock()

	go func() {
		C.virgilRunTapLoop(tap)
	}()

	return m, nil
}

// Events returns the channel of hotkey events.
func (h *HotkeyManager) Events() <-chan HotkeyEvent {
	return h.events
}

// Close stops the hotkey manager and releases resources.
func (h *HotkeyManager) Close() {
	globalHotkeyMu.Lock()
	if globalHotkeyManager == h {
		globalHotkeyManager = nil
	}
	globalHotkeyMu.Unlock()
	close(h.events)
}
