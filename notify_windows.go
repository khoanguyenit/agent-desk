//go:build windows

package main

import (
	"os"
	"path/filepath"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Windows shows a program's notifications only if three switches allow it, and
// says nothing when one of them does not: the notification is accepted and
// dropped. So the app looks at the switches itself.

const (
	pushKey     = `Software\Microsoft\Windows\CurrentVersion\PushNotifications`
	pushPolicy  = `Software\Policies\Microsoft\Windows\CurrentVersion\PushNotifications`
	perAppKey   = `Software\Microsoft\Windows\CurrentVersion\Notifications\Settings\`
	settingsWay = "Settings → System → Notifications"
)

// dword reads a number from the registry. ok is false when it is not there,
// which for each of these switches means "not switched off".
func dword(root registry.Key, path, name string) (value uint64, ok bool) {
	k, err := registry.OpenKey(root, path, registry.QUERY_VALUE)
	if err != nil {
		return 0, false
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue(name)
	return v, err == nil
}

// toastBlock says why Windows will not show this app's notifications, or
// returns "" when nothing it can see stands in their way. "Do not disturb"
// is not among the things it can see.
func toastBlock() string {
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		if v, ok := dword(root, pushPolicy, "NoToastApplicationNotification"); ok && v != 0 {
			return "Thông báo của Windows đang bị tắt bằng chính sách của máy (Turn off toast notifications), nên Windows không hiện thông báo của app."
		}
	}
	if v, ok := dword(registry.CURRENT_USER, pushKey, "ToastEnabled"); ok && v == 0 {
		return "Windows đang tắt thông báo của mọi ứng dụng, nên không hiện thông báo của app. Bật lại ở " + settingsWay + "."
	}
	// Wails registers the app under the name of its executable.
	if exe, err := os.Executable(); err == nil {
		name := filepath.Base(exe)
		if v, ok := dword(registry.CURRENT_USER, perAppKey+name, "Enabled"); ok && v == 0 {
			return "Windows đang tắt thông báo của riêng app này. Bật lại ở " + settingsWay + ", mục " + name + "."
		}
	}
	return ""
}

var (
	user32            = windows.NewLazySystemDLL("user32.dll")
	procFlashWindowEx = user32.NewProc("FlashWindowEx")

	// The callback handed to EnumWindows is made once: Windows callbacks made
	// from Go are never given back.
	findOnce  sync.Once
	findProc  uintptr
	findMu    sync.Mutex
	findFound windows.HWND
)

// The class Wails gives its main window.
const mainWindowClass = "wailsWindow"

// mainWindow finds the app's own window among the windows on the desktop.
func mainWindow() windows.HWND {
	findOnce.Do(func() {
		pid := windows.GetCurrentProcessId()
		findProc = windows.NewCallback(func(h windows.HWND, _ uintptr) uintptr {
			var owner uint32
			if _, err := windows.GetWindowThreadProcessId(h, &owner); err != nil || owner != pid {
				return 1
			}
			var class [64]uint16
			n, err := windows.GetClassName(h, &class[0], int32(len(class)))
			if err != nil || windows.UTF16ToString(class[:n]) != mainWindowClass {
				return 1
			}
			findFound = h
			return 0 // found: stop looking
		})
	})
	findMu.Lock()
	defer findMu.Unlock()
	findFound = 0
	// EnumWindows reports an error when the callback stops it early; what
	// counts is whether the window was found.
	_ = windows.EnumWindows(findProc, nil)
	return findFound
}

type flashInfo struct {
	size    uint32
	hwnd    windows.HWND
	flags   uint32
	count   uint32
	timeout uint32
}

const (
	flashTray      = 0x2 // the taskbar button
	flashUntilSeen = 0xC // keep on until the window comes to the front
)

// flashWindow makes the app's taskbar button flash until the window is
// brought forward. It does nothing while the window is already in front: the
// user is looking at it.
func flashWindow() {
	hwnd := mainWindow()
	if hwnd == 0 || windows.GetForegroundWindow() == hwnd {
		return
	}
	info := flashInfo{hwnd: hwnd, flags: flashTray | flashUntilSeen}
	info.size = uint32(unsafe.Sizeof(info))
	_, _, _ = procFlashWindowEx.Call(uintptr(unsafe.Pointer(&info)))
}
