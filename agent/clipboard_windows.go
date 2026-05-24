//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

var (
	libkernel32            = syscall.MustLoadDLL("kernel32.dll")
	procOpenClip           = libuser32.MustFindProc("OpenClipboard")
	procCloseClip          = libuser32.MustFindProc("CloseClipboard")
	procEmptyClip          = libuser32.MustFindProc("EmptyClipboard")
	procGetClipData        = libuser32.MustFindProc("GetClipboardData")
	procSetClipData        = libuser32.MustFindProc("SetClipboardData")
	procGlobalAlloc        = libkernel32.MustFindProc("GlobalAlloc")
	procGlobalLock         = libkernel32.MustFindProc("GlobalLock")
	procGlobalUnlock       = libkernel32.MustFindProc("GlobalUnlock")
	procIsClipFmtAvailable = libuser32.MustFindProc("IsClipboardFormatAvailable")
)

const (
	CF_TEXT         = 1
	CF_UNICODETEXT  = 13
	GMEM_MOVEABLE   = 2
)

type winClipboard struct{}

func newClipboard() ClipboardManager {
	return &winClipboard{}
}

func (c *winClipboard) GetText() string {
	syscall.Syscall(procOpenClip.Addr(), 1, 0, 0, 0)

	ret, _, _ := syscall.Syscall(procIsClipFmtAvailable.Addr(), 1, CF_UNICODETEXT, 0, 0)
	if ret == 0 {
		syscall.Syscall(procCloseClip.Addr(), 0, 0, 0, 0)
		return ""
	}

	hData, _, _ := syscall.Syscall(procGetClipData.Addr(), 1, CF_UNICODETEXT, 0, 0)
	if hData == 0 {
		syscall.Syscall(procCloseClip.Addr(), 0, 0, 0, 0)
		return ""
	}

	pData, _, _ := syscall.Syscall(procGlobalLock.Addr(), 1, hData, 0, 0)
	if pData == 0 {
		syscall.Syscall(procCloseClip.Addr(), 0, 0, 0, 0)
		return ""
	}

	text := syscall.UTF16ToString((*[1 << 20]uint16)(unsafe.Pointer(pData))[:])

	syscall.Syscall(procGlobalUnlock.Addr(), 1, hData, 0, 0)
	syscall.Syscall(procCloseClip.Addr(), 0, 0, 0, 0)

	return text
}

func (c *winClipboard) SetText(text string) error {
	syscall.Syscall(procOpenClip.Addr(), 1, 0, 0, 0)
	syscall.Syscall(procEmptyClip.Addr(), 0, 0, 0, 0)

	utf16, _ := syscall.UTF16FromString(text)
	size := len(utf16) * 2

	hMem, _, _ := syscall.Syscall(procGlobalAlloc.Addr(), 2, GMEM_MOVEABLE, uintptr(size), 0)
	pMem, _, _ := syscall.Syscall(procGlobalLock.Addr(), 1, hMem, 0, 0)

	dst := unsafe.Slice((*uint16)(unsafe.Pointer(pMem)), len(utf16))
	copy(dst, utf16)

	syscall.Syscall(procGlobalUnlock.Addr(), 1, hMem, 0, 0)
	syscall.Syscall(procSetClipData.Addr(), 2, CF_UNICODETEXT, hMem, 0)
	syscall.Syscall(procCloseClip.Addr(), 0, 0, 0, 0)

	return nil
}
