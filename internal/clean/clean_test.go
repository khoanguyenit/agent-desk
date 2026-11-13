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

// fakeStripeKey has the shape of a Stripe secret key and is made up. It is put
// together at run time so that no scanner for leaked keys mistakes this file
// for one that holds a real key.
var fakeStripeKey = "sk_" + "live_" + "51Habcdefghijklmnopqrstuv"

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
		// A telling word at the end of a longer name.
		{"snake case assignment", `db_password=hunter2xyz ./migrate up`, "hunter2xyz", "./migrate up"},
		{"camel case assignment", `dbPassword=hunter2xyz node seed.js`, "hunter2xyz", "node seed.js"},
		{"yaml upper with colon", "database:\n  DB_PASSWORD: hunter2xyz\n  DB_HOST: db01", "hunter2xyz", "DB_HOST: db01"},
		{"yaml lower with colon", "db_password: hunter2xyz", "hunter2xyz", "db_password:"},
		{"mysql pwd variable", `export MYSQL_PWD=hunter2xyz; mysql -u app`, "hunter2xyz", "mysql -u app"},
		{"aws secret access key", `aws_secret_access_key = wJalrXUtnFEMIK7MDENGbPxRfiCYEXAMPLEKEY`, "wJalrXUtnFEMIK7MDENGbPxRfiCYEXAMPLEKEY", "aws_secret_access_key"},
		{"npmrc auth token", `//registry.npmjs.org/:_authToken=npmtokenvalue123`, "npmtokenvalue123", "registry.npmjs.org"},
		// Tools with a password flag of their own.
		{"sqlcmd -P", `sqlcmd -S db01 -U sa -P Hunter2xyz! -Q "select 1"`, "Hunter2xyz!", `-Q "select 1"`},
		{"bcp -P", `bcp dbo.trades out t.dat -S db01 -U sa -P Hunter2xyz -c`, "Hunter2xyz", "-c"},
		{"curl -u", `curl -u admin:hunter2xyz https://api.internal/v1`, "hunter2xyz", "https://api.internal/v1"},
		{"curl --user quoted", `curl --user "admin:hunter2xyz" https://api.internal/v1`, "hunter2xyz", "https://api.internal/v1"},
		{"redis-cli -a", `redis-cli -h cache -a hunter2xyz ping`, "hunter2xyz", "ping"},
		{"docker login -p", `docker login -u me -p hunter2xyz registry.internal`, "hunter2xyz", "registry.internal"},
		{"go mysql dsn", `dsn := "root:hunter2xyz@tcp(127.0.0.1:3306)/trading?parseTime=true"`, "hunter2xyz", "@tcp(127.0.0.1:3306)/trading"},
		{"cookie header", `curl -H "Cookie: session=abcd1234efgh5678ijkl; theme=dark" https://x`, "abcd1234efgh5678ijkl", "https://x"},
		{"ado connection string", `Server=db;User Id=sa;Password=hunter2xyz;Encrypt=true`, "hunter2xyz", "Encrypt=true"},
		// Shapes of their own.
		{"stripe secret key", "STRIPE=x " + fakeStripeKey + " charged", fakeStripeKey, "charged"},
		{"google api key", `key AIzaSyA1234567890abcdefghijklmnopqrstuv in maps.js`, "AIzaSyA1234567890abcdefghijklmnopqrstuv", "in maps.js"},
		{"gitlab token", `push with glpat-abcdefghij0123456789 now`, "glpat-abcdefghij0123456789", "now"},
		{"npm token", `publish with npm_abcdefghijklmnopqrstuvwxyz0123456789 now`, "npm_abcdefghijklmnopqrstuvwxyz0123456789", "now"},
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
		// Code that names a secret without holding one.
		`secret := os.Getenv("APP_SECRET")`,
		`if password == "" {`,
		`apiKey := cfg.Get("api_key")`,
		// Flags that look like a password flag in other tools.
		"docker run -u 1000:1000 -p 5432:5432 postgres",
		"psql -h db -p 5432 -U app trading",
		"sqlcmd -S db01 -p -Q \"select 1\"",
		"redis-cli -h cache ping",
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
