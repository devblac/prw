//go:build windows

package main

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32                       = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleScreenBufferInfo = kernel32.NewProc("GetConsoleScreenBufferInfo")
	procGetConsoleMode             = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode             = kernel32.NewProc("SetConsoleMode")
)

type consoleCoord struct{ X, Y int16 }

type consoleRect struct{ Left, Top, Right, Bottom int16 }

type consoleScreenBufferInfo struct {
	Size              consoleCoord
	CursorPosition    consoleCoord
	Attributes        uint16
	Window            consoleRect
	MaximumWindowSize consoleCoord
}

// consoleWidth is the visible width of the Windows console, or 0.
func consoleWidth() int {
	var info consoleScreenBufferInfo
	r, _, _ := procGetConsoleScreenBufferInfo.Call(os.Stdout.Fd(), uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		return 0
	}
	return int(info.Window.Right-info.Window.Left) + 1
}

// enableTerminalEscapes turns on ANSI escape sequences (clearing the screen,
// hyperlinks) in consoles that need it; Windows Terminal already has them.
func enableTerminalEscapes() {
	const enableVirtualTerminalProcessing = 0x0004
	var mode uint32
	if r, _, _ := procGetConsoleMode.Call(os.Stdout.Fd(), uintptr(unsafe.Pointer(&mode))); r == 0 {
		return
	}
	procSetConsoleMode.Call(os.Stdout.Fd(), uintptr(mode|enableVirtualTerminalProcessing))
}
