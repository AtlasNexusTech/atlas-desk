//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

var (
	libuser32          = syscall.MustLoadDLL("user32.dll")
	procSetCursorPos   = libuser32.MustFindProc("SetCursorPos")
	procmouseEvent     = libuser32.MustFindProc("mouse_event")
	prockeybdEvent     = libuser32.MustFindProc("keybd_event")
	procMapVirtualKey  = libuser32.MustFindProc("MapVirtualKeyW")
)

const (
	MOUSEEVENTF_LEFTDOWN   = 0x0002
	MOUSEEVENTF_LEFTUP     = 0x0004
	MOUSEEVENTF_RIGHTDOWN  = 0x0008
	MOUSEEVENTF_RIGHTUP    = 0x0010
	MOUSEEVENTF_WHEEL      = 0x0800

	KEYEVENTF_KEYUP = 0x0002
)

func moveMouse(x, y int) {
	syscall.Syscall(procSetCursorPos.Addr(), 2, uintptr(x), uintptr(y), 0)
}

func clickMouse(btn string) {
	var down, up uintptr
	switch btn {
	case "right":
		down, up = MOUSEEVENTF_RIGHTDOWN, MOUSEEVENTF_RIGHTUP
	default:
		down, up = MOUSEEVENTF_LEFTDOWN, MOUSEEVENTF_LEFTUP
	}
	syscall.Syscall6(procmouseEvent.Addr(), 5, down, 0, 0, 0, 0, 0)
	syscall.Syscall6(procmouseEvent.Addr(), 5, up, 0, 0, 0, 0, 0)
}

func pressKey(key string) {
	vk := keyToVK(key)
	if vk == 0 { return }
	syscall.Syscall(prockeybdEvent.Addr(), 4, uintptr(vk), 0, 0, 0)
	syscall.Syscall(prockeybdEvent.Addr(), 4, uintptr(vk), 0, KEYEVENTF_KEYUP, 0)
}

func typeText(text string) {
	for _, c := range text {
		vk := charToVK(c)
		if vk == 0 { continue }
		if c >= 'A' && c <= 'Z' {
			syscall.Syscall(prockeybdEvent.Addr(), 4, 0x10, 0, 0, 0) // VK_SHIFT
		}
		syscall.Syscall(prockeybdEvent.Addr(), 4, uintptr(vk), 0, 0, 0)
		syscall.Syscall(prockeybdEvent.Addr(), 4, uintptr(vk), 0, KEYEVENTF_KEYUP, 0)
		if c >= 'A' && c <= 'Z' {
			syscall.Syscall(prockeybdEvent.Addr(), 4, 0x10, 0, KEYEVENTF_KEYUP, 0)
		}
	}
}

func scrollMouse(dy int) {
	syscall.Syscall6(procmouseEvent.Addr(), 5, MOUSEEVENTF_WHEEL, 0, 0, uintptr(dy*120), 0, 0)
}

func keyToVK(key string) int {
	m := map[string]int{
		"enter": 0x0D, "escape": 0x1B, "backspace": 0x08, "tab": 0x09,
		"space": 0x20, "left": 0x25, "up": 0x26, "right": 0x27, "down": 0x28,
		"delete": 0x2E, "home": 0x24, "end": 0x23, "pageup": 0x21, "pagedown": 0x22,
		"f1": 0x70, "f2": 0x71, "f3": 0x72, "f4": 0x73, "f5": 0x74,
		"f6": 0x75, "f7": 0x76, "f8": 0x77, "f9": 0x78, "f10": 0x79,
		"f11": 0x7A, "f12": 0x7B,
		"control": 0x11, "alt": 0x12, "shift": 0x10, "meta": 0x5B,
	}
	if v, ok := m[key]; ok { return v }
	if len(key) == 1 { return charToVK(rune(key[0])) }
	return 0
}

func charToVK(c rune) int {
	switch {
	case c >= 'a' && c <= 'z': return int(c - 'a' + 0x41)
	case c >= 'A' && c <= 'Z': return int(c - 'A' + 0x41)
	case c >= '0' && c <= '9': return int(c - '0' + 0x30)
	}
	r, _, _ := syscall.Syscall(procMapVirtualKey.Addr(), 2, uintptr(c), 1, 0)
	return int(r)
}

var _ = unsafe.Pointer(nil)
