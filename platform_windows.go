//go:build windows

package main

import (
	"context"

	"golang.org/x/sys/windows"
)

// openURI hands a URI to the shell, which starts the program registered for
// its scheme. No command line is involved, so nothing in the URI is parsed
// as a command.
func openURI(_ context.Context, uri string) error {
	verb, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	target, err := windows.UTF16PtrFromString(uri)
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, verb, target, nil, nil, windows.SW_SHOWNORMAL)
}

// fatal reports a start-up failure. A windowed program has no console, so
// the message goes in a dialog.
func fatal(message string) {
	text, err1 := windows.UTF16PtrFromString(message)
	caption, err2 := windows.UTF16PtrFromString("Agent Desk")
	if err1 == nil && err2 == nil {
		_, _ = windows.MessageBox(0, text, caption, windows.MB_OK|windows.MB_ICONERROR)
	}
}
