package pane

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestSplitGivesTheReviewItsMinimumSize(t *testing.T) {
	cases := []struct {
		w, h  int
		below bool
	}{
		{0, 0, true},     // unknown size
		{200, 40, false}, // wide: side by side
		{110, 24, false}, // below would leave 11 rows; beside leaves 54x24
		{100, 50, true},  // beside would leave 49 columns; below leaves 100x24
		{80, 24, true},   // neither fits; narrow panes split below
		{130, 20, false}, // wide enough beside, too short below
		{119, 60, true},  // below fits and the pane is not roomy
	}
	for _, c := range cases {
		if got := below(c.w, c.h); got != c.below {
			t.Errorf("below(%d, %d) = %v, want %v", c.w, c.h, got, c.below)
		}
	}
}

func TestITermDetection(t *testing.T) {
	found := func(string) (string, error) { return "/usr/bin/osascript", nil }
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	vars := map[string]string{"TERM_PROGRAM": "iTerm.app", "ITERM_SESSION_ID": "w0t1p0:ABC-123"}

	h, ok := detect(env(vars), "darwin", found)
	if !ok || h.Name() != "iterm" || h.(iterm).session != "ABC-123" {
		t.Fatalf("iTerm with a session id should be detected for that session, got %v %v", h, ok)
	}
	delete(vars, "ITERM_SESSION_ID")
	if _, ok := detect(env(vars), "darwin", found); ok {
		t.Fatal("iTerm without ITERM_SESSION_ID would split whatever window has focus")
	}
	vars["ITERM_SESSION_ID"] = "w0t1p0:ABC-123"
	if _, ok := detect(env(vars), "linux", found); ok {
		t.Fatal("iTerm is macOS only")
	}
	missing := func(string) (string, error) { return "", errors.New("not found") }
	if _, ok := detect(env(vars), "darwin", missing); ok {
		t.Fatal("iTerm needs osascript")
	}
}

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{
		"plain/path-1.2:x=y@z": "plain/path-1.2:x=y@z",
		"":                     "''",
		"has space":            "'has space'",
		"it's":                 `'it'\''s'`,
		"$(rm -rf ~)":          "'$(rm -rf ~)'",
	} {
		if got := ShellQuote(in); got != want {
			t.Errorf("ShellQuote(%q) = %q, want %q", in, got, want)
		}
	}
	if got := shellLine([]string{"/bin/sift", "review", "--home", "/a b"}); got != "/bin/sift review --home '/a b'" {
		t.Errorf("shellLine = %q", got)
	}
}

func TestAppleStringEscapes(t *testing.T) {
	if got := appleString(`say "hi" \ bye`); got != `"say \"hi\" \\ bye"` {
		t.Errorf("appleString = %s", got)
	}
}

// fakeHost writes an executable that records its arguments, one call per line, and prints out for
// display-message calls. It fails with stderr "boom" when fail is set.
func fakeHost(t *testing.T, out string, fail bool) (path, log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "calls")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + ShellQuote(log) + "\n" +
		"case \"$1\" in display-message) printf '%s\\n' " + ShellQuote(out) + ";; esac\n"
	if fail {
		script += "echo boom >&2\nexit 1\n"
	}
	path = filepath.Join(dir, "host")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, log
}

func calls(t *testing.T, log string) []string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func TestTmuxSplitsByPaneSize(t *testing.T) {
	argv := []string{"/bin/sift", "review", "--run", "r1"}
	for size, flag := range map[string]string{"200 50": "-h", "100 60": "-v", "garbage": "-v"} {
		path, log := fakeHost(t, size, false)
		h := tmux{path: path, pane: "%3"}
		if h.Name() != "tmux" {
			t.Fatalf("name = %q", h.Name())
		}
		if err := h.Open(context.Background(), argv); err != nil {
			t.Fatal(err)
		}
		got := calls(t, log)
		want := []string{"display-message -p -t %3 #{pane_width} #{pane_height}", "split-window " + flag + " -t %3 /bin/sift review --run r1"}
		if !slices.Equal(got, want) {
			t.Errorf("pane %q: calls = %q, want %q", size, got, want)
		}
	}
}

func TestHostFailureCarriesStderr(t *testing.T) {
	path, _ := fakeHost(t, "200 50", true)
	err := tmux{path: path}.Open(context.Background(), []string{"sift"})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want the host's stderr", err)
	}
}

func TestOrcaSplitsByTerminalSize(t *testing.T) {
	defer func(f func() (int, int)) { ttySize = f }(ttySize)
	for size, divider := range map[[2]int]string{{200, 50}: "vertical", {0, 0}: "horizontal"} {
		ttySize = func() (int, int) { return size[0], size[1] }
		path, log := fakeHost(t, "", false)
		h := orca{path: path, handle: "term-1"}
		if h.Name() != "orca" {
			t.Fatalf("name = %q", h.Name())
		}
		if err := h.Open(context.Background(), []string{"/bin/sift", "review"}); err != nil {
			t.Fatal(err)
		}
		want := "terminal split --terminal term-1 --direction " + divider + " --command /bin/sift review && exit --json"
		if got := calls(t, log); len(got) != 1 || got[0] != want {
			t.Errorf("size %v: calls = %q, want %q", size, got, want)
		}
	}
}

func TestITermSplitsTheAgentsSession(t *testing.T) {
	path, log := fakeHost(t, "", false)
	h := iterm{path: path, session: "ABC-123"}
	if h.Name() != "iterm" {
		t.Fatalf("name = %q", h.Name())
	}
	if err := h.Open(context.Background(), []string{"/bin/sift", "review", "--home", `/a "b"`}); err != nil {
		t.Fatal(err)
	}
	script := strings.Join(calls(t, log), "\n")
	for _, want := range []string{`unique id of s is "ABC-123"`, `split vertically with default profile command "/bin/sift review --home '/a \"b\"'"`} {
		if !strings.Contains(script, want) {
			t.Errorf("script lacks %q:\n%s", want, script)
		}
	}
}

func TestDetectFindsNothingOutsideAHost(t *testing.T) {
	if h, ok := Detect(func(string) string { return "" }); ok {
		t.Fatalf("detected %v with no host environment", h.Name())
	}
}
