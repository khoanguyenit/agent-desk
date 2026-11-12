package clean

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// invisible builds a string from code points, so that this file never
// contains the invisible characters it tests against.
func invisible(points ...rune) string { return string(points) }

func TestText(t *testing.T) {
	var (
		esc      = invisible(0x1B)
		bell     = invisible(0x07)
		nul      = invisible(0x00)
		override = invisible(0x202E) // right-to-left override
		isolate  = invisible(0x2066)
		popIso   = invisible(0x2069)
		rtlMark  = invisible(0x200F)
		bom      = invisible(0xFEFF)
	)
	cases := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"collapses whitespace", "  a \n\t b  ", 0, "a b"},
		{"drops control characters", "ok" + esc + "]52;c;ZXZpbA==" + bell + " done" + nul, 0, "ok]52;c;ZXZpbA== done"},
		{"drops bidirectional overrides", "safe" + override + "gnp.exe", 0, "safegnp.exe"},
		{"drops isolates and marks", "a" + isolate + "b" + popIso + "c" + rtlMark + "d" + bom, 0, "abcd"},
		{"keeps Vietnamese", "sửa lỗi  timeout", 0, "sửa lỗi timeout"},
		{"empty", "", 10, ""},
		{"only whitespace", " \n\t ", 10, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Text(c.in, c.max); got != c.want {
				t.Fatalf("Text(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}

	long := strings.Repeat("ế", 300)
	got := Text(long, 180)
	if utf8.RuneCountInString(got) != 180 || !strings.HasSuffix(got, "…") {
		t.Fatalf("clipped to %d runes, suffix ok = %v", utf8.RuneCountInString(got), strings.HasSuffix(got, "…"))
	}
	if got := Text("exactly ten", 11); got != "exactly ten" {
		t.Fatalf("text at the limit was changed: %q", got)
	}
}

func TestRedactMasksCredentials(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		secret string // must not survive
		keep   string // must survive
	}{
		{"env assignment", `PGPASSWORD=hunter2 psql -h db.internal -U app`, "hunter2", "psql -h db.internal"},
		{"quoted env assignment", `export API_TOKEN="abc def" && run`, "abc def", "&& run"},
		{"authorization header", `curl -H "Authorization: Bearer abc.def-123" https://api.example.com/v1`, "abc.def-123", "https://api.example.com/v1"},
		{"bare bearer", `sent header bearer AbCdEf123456 to the gateway`, "AbCdEf123456", "to the gateway"},
		{"url query token", `https://example.com/cb?state=ok&token=s3cr3tvalue&next=/home`, "s3cr3tvalue", "next=/home"},
		{"url signature", `GET https://files.example.com/a.zip?sig=ZZZ999&exp=1`, "ZZZ999", "exp=1"},
		{"url userinfo", `git clone https://deploy:p4ssw0rd@git.example.com/repo.git`, "p4ssw0rd", "git.example.com/repo.git"},
		{"key value", `password: "correct horse" in config`, "correct horse", "in config"},
		{"json key", `{"client_secret":"shhh-123","id":7}`, "shhh-123", `"id":7`},
		{"flag with value", `tool --password s3cr3t --verbose`, "s3cr3t", "--verbose"},
		{"flag with equals", `tool --api-key=KEY12345 run`, "KEY12345", "run"},
		{"mysql inline password", `mysql -u root -pS3cret! -h 10.0.0.5 trading`, "S3cret!", "trading"},
		{"aws key id", `key AKIAIOSFODNN7EXAMPLE was used`, "AKIAIOSFODNN7EXAMPLE", "was used"},
		{"sk token", `the key is sk-ant-api03-abcdefghijklmnop today`, "sk-ant-api03-abcdefghijklmnop", "today"},
		{"github token", `push with ghp_abcdefghijklmnopqrstuvwxyz0123`, "ghp_abcdefghijklmnopqrstuvwxyz0123", "push with"},
		{"jwt", `cookie eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4 ok`, "eyJhbGciOiJIUzI1NiJ9", "ok"},
		{"private key", "here -----BEGIN RSA PRIVATE KEY-----\nMIIEow\n-----END RSA PRIVATE KEY-----", "MIIEow", "here"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Redact(c.in)
			if strings.Contains(got, c.secret) {
				t.Fatalf("secret survived: %q", got)
			}
			if !strings.Contains(got, c.keep) {
				t.Fatalf("lost surrounding text %q: %q", c.keep, got)
			}
			if !strings.Contains(got, Mask) {
				t.Fatalf("nothing was masked: %q", got)
			}
		})
	}
}

// Ordinary text about tokens, keys and passwords must stay readable; this app
// in particular talks about tokens all the time.
func TestRedactLeavesOrdinaryTextAlone(t *testing.T) {
	for _, s := range []string{
		"Tổng token: 2.7M, cache đọc 93%",
		"token usage went from 105.3k to 219.9k",
		"Run the unit tests for the booking package",
		"internal/handler/hold_release.go",
		"go test ./... -run TestKey -count=1",
		"The password field is validated in auth.go",
		"docker run -p 8080:80 nginx",
		"key issuing UI review: no blocking findings",
		"https://example.com/docs?page=2&lang=vi",
		"grep -rn 'secret' internal/ | head",
	} {
		if got := Redact(s); got != s {
			t.Errorf("Redact changed ordinary text:\n  in:  %q\n  out: %q", s, got)
		}
	}
}

func TestSnippetRedactsBeforeClipping(t *testing.T) {
	// The secret straddles the clip point of the raw text. Clipping first
	// would leave its beginning on screen.
	in := strings.Repeat("x", 160) + " PASSWORD=supersecretvalue-that-is-long"
	got := Snippet(in, 180)
	if strings.Contains(got, "supers") {
		t.Fatalf("part of the secret is visible: %q", got)
	}
}
