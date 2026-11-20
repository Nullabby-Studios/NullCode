//go:build windows

package nullcode

import "golang.org/x/sys/windows"

// enableVT turns on ANSI/VT processing for the Windows console. x/term
// handles raw mode; this makes the escape sequences used by the renderer
// and the arrow-key input sequences work.
func enableVT() {
	out, err := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE)
	if err == nil {
		var mode uint32
		if windows.GetConsoleMode(out, &mode) == nil {
			_ = windows.SetConsoleMode(out, mode|
				windows.ENABLE_PROCESSED_OUTPUT|
				windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
		}
	}
	in, err := windows.GetStdHandle(windows.STD_INPUT_HANDLE)
	if err == nil {
		var mode uint32
		if windows.GetConsoleMode(in, &mode) == nil {
			_ = windows.SetConsoleMode(in, mode|windows.ENABLE_VIRTUAL_TERMINAL_INPUT)
		}
	}
}
