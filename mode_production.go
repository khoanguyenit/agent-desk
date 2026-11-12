//go:build production

package main

import "monitor-agent/internal/monitor"

// contentSecurityPolicy is what the built app enforces on its one page: no
// inline or remote script, no request to any origin (the page talks to Go
// through the WebView's message channel, not the network), and no HTML
// parsing of strings at all. Text taken from transcripts therefore cannot
// run, load anything, or be sent anywhere, even if a rendering bug were
// introduced later.
const contentSecurityPolicy = "default-src 'none'; " +
	"script-src 'self'; " +
	"style-src 'self'; " +
	"font-src 'self'; " +
	"img-src 'self'; " +
	"connect-src 'none'; " +
	"base-uri 'none'; " +
	"form-action 'none'; " +
	"frame-ancestors 'none'; " +
	"require-trusted-types-for 'script'; " +
	"trusted-types 'none'"

// monitorOptions is the built app's: real data, nothing locked. See the
// development build's version for what the lock is.
func monitorOptions() (opts monitor.Options, notice string, locked bool) {
	return monitor.Options{}, "", false
}
