package pane

import (
	"errors"
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
