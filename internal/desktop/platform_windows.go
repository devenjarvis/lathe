//go:build desktop && windows

package desktop

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// singleInstanceUsable: Windows locks with a named mutex, always available.
func singleInstanceUsable() (bool, string) { return true, "" }

var (
	kernel32                  = windows.NewLazySystemDLL("kernel32.dll")
	procGetConsoleProcessList = kernel32.NewProc("GetConsoleProcessList")
	procFreeConsole           = kernel32.NewProc("FreeConsole")
)

// detachConsole lets go of the console Windows opened for this process. The
// release lathe.exe is a console-subsystem binary so the CLI can print, which
// means double-clicking it opens a console next to the app. When this process
// is the only one attached to its console, the console was made for it, so
// it is freed (and closes). A console shared with a shell (`lathe app` typed
// in a terminal) lists two or more processes and is kept.
func detachConsole() {
	if procGetConsoleProcessList.Find() != nil || procFreeConsole.Find() != nil {
		return
	}
	var pids [2]uint32
	n, _, _ := procGetConsoleProcessList.Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	if n == 1 {
		_, _, _ = procFreeConsole.Call()
	}
}
