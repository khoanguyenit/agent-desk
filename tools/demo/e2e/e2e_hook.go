//go:build e2e

package main

// A test-only door into the page. It is compiled only with the "e2e" tag, in
// a throwaway copy of the project, and never into the app that is shipped.
// It listens on the loopback interface, and only when both the port and a
// secret token are given in the environment.

import (
	"context"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

var e2e struct {
	mu      sync.Mutex
	next    int
	waiting map[string]chan string
}

func (a *App) startE2E(ctx context.Context) {
	port, token := os.Getenv("MONITOR_AGENT_E2E_PORT"), os.Getenv("MONITOR_AGENT_E2E_TOKEN")
	if port == "" || token == "" {
		return
	}
	e2e.waiting = map[string]chan string{}
	guard := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.Header.Get("X-E2E-Token") != token {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			h(w, r)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/eval", guard(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		e2e.mu.Lock()
		e2e.next++
		id := strconv.Itoa(e2e.next)
		ch := make(chan string, 1)
		e2e.waiting[id] = ch
		e2e.mu.Unlock()

		js := "(async () => { let out; try { out = {value: await (" + string(body) + ")}; } " +
			"catch (e) { out = {error: String((e && e.stack) || e)}; } " +
			"let s; try { s = JSON.stringify(out); } catch (e) { s = JSON.stringify({error: 'unserialisable: ' + e}); } " +
			"window.go.main.App.E2EResult(" + strconv.Quote(id) + ", s); })()"
		runtime.WindowExecJS(ctx, js)
		select {
		case res := <-ch:
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = io.WriteString(w, res)
		case <-time.After(20 * time.Second):
			e2e.mu.Lock()
			delete(e2e.waiting, id)
			e2e.mu.Unlock()
			http.Error(w, "the page did not answer", http.StatusGatewayTimeout)
		}
	}))
	mux.HandleFunc("/size", guard(func(w http.ResponseWriter, r *http.Request) {
		width, _ := strconv.Atoi(r.URL.Query().Get("w"))
		height, _ := strconv.Atoi(r.URL.Query().Get("h"))
		if width > 0 && height > 0 {
			runtime.WindowSetSize(ctx, width, height)
		}
		_, _ = io.WriteString(w, "ok")
	}))
	// The trust question for a folder, as "Thêm project" asks it once the
	// folder has been picked. The folder dialog itself is left out of a
	// recording: it shows the folders of whoever is at the machine.
	mux.HandleFunc("/trust", guard(func(w http.ResponseWriter, r *http.Request) {
		dir := r.URL.Query().Get("dir")
		go a.trust(dir)
		_, _ = io.WriteString(w, "ok")
	}))
	go func() { _ = http.ListenAndServe("127.0.0.1:"+port, mux) }()
}

// E2EResult is called by the page with the outcome of an evaluation.
func (a *App) E2EResult(id, result string) {
	e2e.mu.Lock()
	ch := e2e.waiting[id]
	delete(e2e.waiting, id)
	e2e.mu.Unlock()
	if ch != nil {
		ch <- result
	}
}
