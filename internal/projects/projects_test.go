package projects

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAddListRemoveSurviveReopen(t *testing.T) {
	file := filepath.Join(t.TempDir(), "nested", "projects.json")
	a, b := t.TempDir(), t.TempDir()

	s, err := Open(file)
	if err != nil {
		t.Fatalf("Open with no file: %v", err)
	}
	if got := s.List(); len(got) != 0 {
		t.Fatalf("new store lists %v", got)
	}
	for _, dir := range []string{a, b, a} {
		if err := s.Add(dir); err != nil {
			t.Fatalf("Add(%q): %v", dir, err)
		}
	}
	if got := s.List(); len(got) != 2 || Key(got[0]) != Key(a) || Key(got[1]) != Key(b) {
		t.Fatalf("after adding a, b, a the list is %v", got)
	}

	again, err := Open(file)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := again.List(); len(got) != 2 || !again.Has(a) || !again.Has(b) {
		t.Fatalf("reopened list is %v", got)
	}

	if err := again.Remove(a); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := again.Remove(a); err != nil {
		t.Fatalf("removing twice: %v", err)
	}
	if again.Has(a) || !again.Has(b) {
		t.Fatalf("after removing a the list is %v", again.List())
	}
	if _, err := os.Stat(a); err != nil {
		t.Fatalf("Remove touched the folder itself: %v", err)
	}
	if _, err := os.Stat(file + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temporary file left behind: %v", err)
	}
}

func TestAddRefusesWhatIsNotAFolder(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(plain, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, _ := Open(filepath.Join(dir, "projects.json"))

	for name, target := range map[string]string{
		"empty":    "",
		"blank":    "   ",
		"relative": "some/folder",
		"missing":  filepath.Join(dir, "does-not-exist"),
		"a file":   plain,
	} {
		if err := s.Add(target); err == nil {
			t.Errorf("Add(%s) was accepted", name)
		}
	}
	if got := s.List(); len(got) != 0 {
		t.Fatalf("refused folders were saved: %v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "projects.json")); !os.IsNotExist(err) {
		t.Fatalf("a refused Add wrote the list: %v", err)
	}
}

func TestSpellingsOfOneFolderAreOneEntry(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(filepath.Join(t.TempDir(), "projects.json"))
	if err := s.Add(dir); err != nil {
		t.Fatal(err)
	}

	spellings := []string{dir + string(filepath.Separator), filepath.Join(dir, "sub", "..")}
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		spellings = append(spellings, strings.ToUpper(dir))
	}
	for _, other := range spellings {
		if !s.Has(other) {
			t.Errorf("Has(%q) is false for a folder that is listed", other)
		}
		if err := s.Add(other); err != nil {
			t.Errorf("Add(%q): %v", other, err)
		}
	}
	if got := s.List(); len(got) != 1 {
		t.Fatalf("one folder is listed %d times: %v", len(got), got)
	}
}

func TestOpenKeepsOnlyUsableEntries(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "projects.json")
	keep := filepath.Join(dir, "keep")
	body := `{"projects": [` +
		`"", "relative/path", ` + quote(keep) + `, ` + quote(keep+string(filepath.Separator)) + `]}`
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := Open(file)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// A folder that has since been deleted stays listed: the user removes it,
	// the app does not decide that for them.
	if got := s.List(); len(got) != 1 || Key(got[0]) != Key(keep) {
		t.Fatalf("list is %v, want only %q", got, keep)
	}
}

func TestOpenReportsAFileItCannotUse(t *testing.T) {
	dir := t.TempDir()

	broken := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(broken, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(broken)
	if err == nil {
		t.Fatal("a broken file was accepted")
	}
	if s == nil || len(s.List()) != 0 {
		t.Fatal("a broken file must still give an empty, usable store")
	}

	huge := filepath.Join(dir, "huge.json")
	if err := os.WriteFile(huge, make([]byte, maxFileBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(huge); err == nil {
		t.Fatal("an oversized file was accepted")
	}
}

// A list that cannot be read is somebody's list all the same: saving a new
// one keeps the old file beside it instead of writing over it.
func TestDamagedListIsKeptAsideNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "projects.json")
	const broken = `{"projects": ["D:\\work\\api", "D:\\work\\web"`
	if err := os.WriteFile(file, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(file)
	if err == nil || !s.Damaged() {
		t.Fatalf("a broken file must be reported: err=%v damaged=%v", err, s.Damaged())
	}
	added := t.TempDir()
	if err := s.Add(added); err != nil {
		t.Fatal(err)
	}
	if s.Damaged() {
		t.Error("still marked damaged after a good save")
	}
	kept, err := os.ReadFile(file + ".bak")
	if err != nil || string(kept) != broken {
		t.Fatalf("the old file was not kept: %q, %v", kept, err)
	}
	again, err := Open(file)
	if err != nil || len(again.List()) != 1 || !again.Has(added) {
		t.Fatalf("the new list: %v, %v", again.List(), err)
	}
}

func TestAddRefusesNetworkAndDevicePaths(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("these path forms are particular to Windows")
	}
	s, _ := Open(filepath.Join(t.TempDir(), "projects.json"))
	for _, target := range []string{`\\192.0.2.1\share\repo`, `//192.0.2.1/share/repo`, `\\?\C:\Windows`, `\\.\C:\Windows`} {
		if err := s.Add(target); err == nil {
			t.Errorf("Add(%q) was accepted", target)
		}
	}
	if got := s.List(); len(got) != 0 {
		t.Fatalf("list = %v", got)
	}
}

func TestStoreWithoutAPathRefusesToSave(t *testing.T) {
	s, err := Open("")
	if err == nil && s != nil {
		if err := s.Add(t.TempDir()); err == nil {
			t.Fatal("Add succeeded with nowhere to save")
		}
		if got := s.List(); len(got) != 0 {
			t.Fatalf("a folder that was not saved is listed: %v", got)
		}
	}
}

// quote writes a path as a JSON string: backslashes in a Windows path must be
// escaped.
func quote(path string) string {
	return `"` + strings.ReplaceAll(path, `\`, `\\`) + `"`
}
