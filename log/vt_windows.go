//go:build windows

package log

import (
	"io"
	"os"
	"syscall"
	"unsafe"
)

const enableVirtualTerminalProcessing = 0x0004

// enableVirtualTerminal asks the Windows console to interpret ANSI escape
// sequences rather than printing them literally.
//
// Windows Terminal enables this itself, but the legacy conhost that still backs
// plain cmd.exe and some IDE terminals does not, and without it every coloured
// log line arrives full of visible escape codes. Calling stdlib syscall
// directly keeps this free of any dependency, which matters because this
// package is linked into every generated application.
func enableVirtualTerminal(w io.Writer) {
	f, ok := w.(*os.File)
	if !ok {
		return
	}

	handle := syscall.Handle(f.Fd())
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getConsoleMode := kernel32.NewProc("GetConsoleMode")
	setConsoleMode := kernel32.NewProc("SetConsoleMode")

	var mode uint32
	ret, _, _ := getConsoleMode.Call(uintptr(handle), uintptr(unsafe.Pointer(&mode)))
	if ret == 0 {
		// Not a console (redirected to a file or pipe); nothing to enable.
		return
	}
	if mode&enableVirtualTerminalProcessing != 0 {
		return
	}
	_, _, _ = setConsoleMode.Call(uintptr(handle), uintptr(mode|enableVirtualTerminalProcessing))
}
