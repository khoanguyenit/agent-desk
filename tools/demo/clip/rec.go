package main

// rec captures the window of one process, a few times a second, into PNG
// frames named by the time they were taken. Only that window is captured,
// through PrintWindow: nothing else on the desktop gets into a frame. A
// dialog the process shows (a message box) is captured too and laid over the
// frame where it stands.
//
//	clip rec -pid N -dir frames [-fps 10]
//
// It runs until a file named "stop" appears in the directory. While it runs:
// a dialog on screen is described in dialog.json (where its buttons are), and
// a file named press-<id> makes it press the dialog button with that id.

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32 = syscall.NewLazyDLL("user32.dll")
	gdi32  = syscall.NewLazyDLL("gdi32.dll")

	pEnumWindows        = user32.NewProc("EnumWindows")
	pEnumChildWindows   = user32.NewProc("EnumChildWindows")
	pGetWindowPID       = user32.NewProc("GetWindowThreadProcessId")
	pIsWindowVisible    = user32.NewProc("IsWindowVisible")
	pGetClassName       = user32.NewProc("GetClassNameW")
	pGetWindowText      = user32.NewProc("GetWindowTextW")
	pGetWindowRect      = user32.NewProc("GetWindowRect")
	pGetClientRect      = user32.NewProc("GetClientRect")
	pClientToScreen     = user32.NewProc("ClientToScreen")
	pPrintWindow        = user32.NewProc("PrintWindow")
	pGetDC              = user32.NewProc("GetDC")
	pReleaseDC          = user32.NewProc("ReleaseDC")
	pGetDlgCtrlID       = user32.NewProc("GetDlgCtrlID")
	pPostMessage        = user32.NewProc("PostMessageW")
	pCreateCompatDC     = gdi32.NewProc("CreateCompatibleDC")
	pCreateCompatBmp    = gdi32.NewProc("CreateCompatibleBitmap")
	pSelectObject       = gdi32.NewProc("SelectObject")
	pDeleteObject       = gdi32.NewProc("DeleteObject")
	pDeleteDC           = gdi32.NewProc("DeleteDC")
	pGetDIBits          = gdi32.NewProc("GetDIBits")
	pSetProcessDPIAware = user32.NewProc("SetProcessDPIAware")
)

type rect struct{ Left, Top, Right, Bottom int32 }
type point struct{ X, Y int32 }

type bitmapInfoHeader struct {
	Size          uint32
	Width, Height int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPels, YPels  int32
	ClrUsed       uint32
	ClrImportant  uint32
}

func className(h uintptr) string {
	buf := make([]uint16, 128)
	pGetClassName.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf)
}

func windowText(h uintptr) string {
	buf := make([]uint16, 256)
	pGetWindowText.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf)
}

func windowRect(h uintptr) rect {
	var r rect
	pGetWindowRect.Call(h, uintptr(unsafe.Pointer(&r)))
	return r
}

// windowsOf lists the visible top-level windows of a process.
//
// The callbacks handed to Windows are made once: each call to NewCallback
// takes a slot that is never given back, and this runs many times a second.
var (
	wantPID  uint32
	found    []uintptr
	onWindow = syscall.NewCallback(func(h, _ uintptr) uintptr {
		var p uint32
		pGetWindowPID.Call(h, uintptr(unsafe.Pointer(&p)))
		if vis, _, _ := pIsWindowVisible.Call(h); p == wantPID && vis != 0 {
			found = append(found, h)
		}
		return 1
	})
	foundButtons []child
	onChild      = syscall.NewCallback(func(h, _ uintptr) uintptr {
		if className(h) == "Button" {
			id, _, _ := pGetDlgCtrlID.Call(h)
			foundButtons = append(foundButtons, child{h, int(id), windowRect(h), windowText(h)})
		}
		return 1
	})
)

func windowsOf(pid uint32) []uintptr {
	wantPID, found = pid, nil
	pEnumWindows.Call(onWindow, 0)
	return found
}

type child struct {
	hwnd uintptr
	id   int
	rect rect
	text string
}

func buttonsOf(dialog uintptr) []child {
	foundButtons = nil
	pEnumChildWindows.Call(dialog, onChild, 0)
	return foundButtons
}

// grab captures one window, frame and all.
func grab(h uintptr) (*image.RGBA, rect) {
	r := windowRect(h)
	w, ht := int(r.Right-r.Left), int(r.Bottom-r.Top)
	if w <= 0 || ht <= 0 {
		return nil, r
	}
	screen, _, _ := pGetDC.Call(0)
	defer pReleaseDC.Call(0, screen)
	mem, _, _ := pCreateCompatDC.Call(screen)
	defer pDeleteDC.Call(mem)
	bmp, _, _ := pCreateCompatBmp.Call(screen, uintptr(w), uintptr(ht))
	defer pDeleteObject.Call(bmp)
	old, _, _ := pSelectObject.Call(mem, bmp)
	pPrintWindow.Call(h, mem, 2) // PW_RENDERFULLCONTENT
	pSelectObject.Call(mem, old)

	bi := bitmapInfoHeader{Size: 40, Width: int32(w), Height: -int32(ht), Planes: 1, BitCount: 32}
	raw := make([]byte, w*ht*4)
	pGetDIBits.Call(mem, bmp, 0, uintptr(ht), uintptr(unsafe.Pointer(&raw[0])), uintptr(unsafe.Pointer(&bi)), 0)
	img := image.NewRGBA(image.Rect(0, 0, w, ht))
	for i := 0; i < len(raw); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = raw[i+2], raw[i+1], raw[i], 255
	}
	// Only the part that is drawn is kept.
	v := visibleRect(h)
	keep := image.Rect(int(v.Left-r.Left), int(v.Top-r.Top), int(v.Right-r.Left), int(v.Bottom-r.Top)).Intersect(img.Bounds())
	if keep.Empty() || keep == img.Bounds() {
		return img, r
	}
	cut := image.NewRGBA(image.Rect(0, 0, keep.Dx(), keep.Dy()))
	draw.Draw(cut, cut.Bounds(), img, keep.Min, draw.Src)
	return cut, v
}

var pDwmGetWindowAttribute = syscall.NewLazyDLL("dwmapi.dll").NewProc("DwmGetWindowAttribute")

// visibleRect is the part of a window that is drawn. The rectangle Windows
// reports for a window also takes in an invisible margin that is there for
// resizing, and a capture shows that margin as a black band.
func visibleRect(h uintptr) rect {
	var r rect
	const extendedFrameBounds = 9
	if hr, _, _ := pDwmGetWindowAttribute.Call(h, extendedFrameBounds, uintptr(unsafe.Pointer(&r)), unsafe.Sizeof(r)); hr != 0 || r.Right <= r.Left {
		return windowRect(h)
	}
	return r
}

type dialogInfo struct {
	Title   string      `json:"title"`
	X       int         `json:"x"`
	Y       int         `json:"y"`
	W       int         `json:"w"`
	H       int         `json:"h"`
	Buttons []buttonPos `json:"buttons"`
}

type buttonPos struct {
	ID   int    `json:"id"`
	Text string `json:"text"`
	// The centre of the button, in pixels of the captured frame.
	X int `json:"x"`
	Y int `json:"y"`
}

type recMeta struct {
	Width   int   `json:"width"`
	Height  int   `json:"height"`
	ClientX int   `json:"clientX"`
	ClientY int   `json:"clientY"`
	Start   int64 `json:"start"`
	End     int64 `json:"end"`
	Frames  int   `json:"frames"`
}

func record(args []string) error {
	fs := flag.NewFlagSet("rec", flag.ExitOnError)
	pid := fs.Int("pid", 0, "process whose window to capture")
	dir := fs.String("dir", "frames", "where to put the frames")
	fps := fs.Int("fps", 10, "captures per second")
	_ = fs.Parse(args)
	if *pid == 0 {
		return fmt.Errorf("no -pid given")
	}
	pSetProcessDPIAware.Call()
	if err := os.MkdirAll(*dir, 0o755); err != nil {
		return err
	}

	// The main window is the one that is not a dialog.
	var main uintptr
	for _, h := range windowsOf(uint32(*pid)) {
		if className(h) != "#32770" && windowText(h) != "" {
			main = h
			break
		}
	}
	if main == 0 {
		return fmt.Errorf("process %d has no window", *pid)
	}
	mr := visibleRect(main)
	var origin point
	pClientToScreen.Call(main, uintptr(unsafe.Pointer(&origin)))
	meta := recMeta{
		Width: int(mr.Right - mr.Left), Height: int(mr.Bottom - mr.Top),
		ClientX: int(origin.X - mr.Left), ClientY: int(origin.Y - mr.Top),
		Start: time.Now().UnixMilli(),
	}

	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	var last []byte
	tick := time.NewTicker(time.Second / time.Duration(*fps))
	defer tick.Stop()
	dialogFile := filepath.Join(*dir, "dialog.json")
	for range tick.C {
		if _, err := os.Stat(filepath.Join(*dir, "stop")); err == nil {
			break
		}
		frame, r := grab(main)
		if frame == nil {
			continue
		}
		// Dialogs of the process are laid over the frame where they stand.
		var seen *dialogInfo
		for _, h := range windowsOf(uint32(*pid)) {
			if h == main || className(h) != "#32770" {
				continue
			}
			img, dr := grab(h)
			if img == nil {
				continue
			}
			at := image.Pt(int(dr.Left-r.Left), int(dr.Top-r.Top))
			draw.Draw(frame, img.Bounds().Add(at), img, image.Point{}, draw.Src)
			info := dialogInfo{Title: windowText(h), X: at.X, Y: at.Y, W: img.Bounds().Dx(), H: img.Bounds().Dy()}
			for _, b := range buttonsOf(h) {
				info.Buttons = append(info.Buttons, buttonPos{
					ID: b.id, Text: strings.ReplaceAll(b.text, "&", ""),
					X: int((b.rect.Left+b.rect.Right)/2 - r.Left), Y: int((b.rect.Top+b.rect.Bottom)/2 - r.Top),
				})
				// A request to press this button.
				ask := filepath.Join(*dir, "press-"+strconv.Itoa(b.id))
				if _, err := os.Stat(ask); err == nil {
					_ = os.Remove(ask)
					pPostMessage.Call(h, 0x0111, uintptr(b.id), b.hwnd) // WM_COMMAND, BN_CLICKED
				}
			}
			seen = &info
		}
		if seen != nil {
			raw, _ := json.Marshal(seen)
			_ = os.WriteFile(dialogFile, raw, 0o644)
		} else {
			_ = os.Remove(dialogFile)
		}

		if bytes.Equal(frame.Pix, last) {
			continue
		}
		last = append(last[:0], frame.Pix...)
		f, err := os.Create(filepath.Join(*dir, fmt.Sprintf("f-%d.png", time.Now().UnixMilli())))
		if err != nil {
			return err
		}
		err = enc.Encode(f, frame)
		f.Close()
		if err != nil {
			return err
		}
		meta.Frames++
	}
	meta.End = time.Now().UnixMilli()
	raw, _ := json.MarshalIndent(meta, "", "  ")
	return os.WriteFile(filepath.Join(*dir, "meta.json"), raw, 0o644)
}
