// clip records the window of a running app and turns the recording into an
// animated GIF. See rec.go and gif.go.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: clip rec|gif [flags]")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "rec":
		err = record(os.Args[2:])
	case "gif":
		err = makeGIF(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "clip:", err)
		os.Exit(1)
	}
}
