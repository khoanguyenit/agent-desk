package main

import (
	"embed"
	"net/http"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

// securityHeaders adds the content security policy to every response of the
// asset server. Sent as a header it applies before any script runs, including
// the ones Wails injects into the page.
func securityHeaders(next http.Handler) http.Handler {
	if contentSecurityPolicy == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

// webviewDataPath keeps the WebView's profile (cache, crash dumps) in the
// local, non-roaming application data folder. What is on screen can end up in
// those files, and the roaming folder is copied to file servers in some
// environments.
func webviewDataPath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "monitor-agent", "WebView2")
}

func main() {
	app := NewApp()

	err := wails.Run(&options.App{
		Title:     "Agent Desk",
		Width:     1240,
		Height:    860,
		MinWidth:  720,
		MinHeight: 480,
		AssetServer: &assetserver.Options{
			Assets:     assets,
			Middleware: securityHeaders,
		},
		// Matches the page's dark background so the window does not flash
		// while the page loads.
		BackgroundColour: &options.RGBA{R: 21, G: 25, B: 34, A: 255},
		OnStartup:        app.startup,
		OnDomReady:       app.domReady,
		OnShutdown:       app.shutdown,
		Bind: []interface{}{
			app,
		},
		// A file dropped on the window would otherwise replace the page with
		// that file, outside the page's security policy.
		DragAndDrop: &options.DragAndDrop{DisableWebViewDrop: true},
		Windows: &windows.Options{
			Theme:               windows.SystemDefault,
			WebviewUserDataPath: webviewDataPath(),
		},
	})
	if err != nil {
		fatal("Agent Desk không khởi động được: " + err.Error())
	}
}
