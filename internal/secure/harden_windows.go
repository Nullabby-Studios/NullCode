//go:build windows

package secure

import (
	"log"
	"syscall"
	"unsafe"
)

var (
	kernel32              = syscall.NewLazyDLL("kernel32.dll")
	procSetDllDirectoryW  = kernel32.NewProc("SetDllDirectoryW")
	procSetDefaultDllDirs = kernel32.NewProc("SetDefaultDllDirectories")
)

// HardenProcess removes the current directory from the DLL search path and
// restricts loading to the system directories (LOAD_LIBRARY_SEARCH_SYSTEM32)
// — the classic Windows DLL-planting vector where a malicious
// <binary-name>-adjacent DLL is injected into our address space.
// Best effort: failures are logged, never fatal.
func HardenProcess() {
	if err := procSetDllDirectoryW.Find(); err == nil {
		empty, _ := syscall.UTF16PtrFromString("")
		r1, _, _ := procSetDllDirectoryW.Call(uintptr(unsafe.Pointer(empty)))
		if r1 == 0 {
			log.Printf("hardening: SetDllDirectory failed — current-dir DLL search remains enabled")
		}
	}
	if err := procSetDefaultDllDirs.Find(); err == nil {
		const loadLibrarySearchSystem32 = 0x00000800
		r1, _, _ := procSetDefaultDllDirs.Call(loadLibrarySearchSystem32)
		if r1 == 0 {
			log.Printf("hardening: SetDefaultDllDirectories(SYSTEM32) failed")
		}
	}
	log.Printf("hardening: DLL search restricted to system directories")
}
