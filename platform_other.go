//go:build !windows

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// openURI asks the desktop to open a URI with whatever handles its scheme.
func openURI(ctx context.Context, uri string) error {
	runtime.BrowserOpenURL(ctx, uri)
	return nil
}

func fatal(message string) {
	fmt.Fprintln(os.Stderr, message)
}
