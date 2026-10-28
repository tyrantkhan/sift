package pane

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// fakeHerdr answers pane layout with layout and pane split with a new pane p9, recording every call. With errJSON
// set, every call fails and prints it on stderr, as Herdr reports errors.
func fakeHerdr(t *testing.T, layout, errJSON string) (path, log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "calls")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + ShellQuote(log) + "\n"
	if errJSON != "" {
		script += "printf '%s' " + ShellQuote(errJSON) + " >&2\nexit 1\n"
	}
	script += "case \"$1 $2\" in\n" +
		"  'pane layout') printf '%s' " + ShellQuote(layout) + ";;\n" +
		"  'pane split') printf '%s' '{\"result\":{\"pane\":{\"pane_id\":\"p9\"}}}';;\n" +
		"esac\n"
	path = filepath.Join(dir, "herdr")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, log
}

func herdrCalls(t *testing.T, log string) []string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func layoutWith(pane string, w, h int) string {
	return `{"result":{"layout":{"panes":[{"pane_id":"other","rect":{"width":10,"height":10}},` +
		`{"pane_id":"` + pane + `","rect":{"width":` + strconv.Itoa(w) + `,"height":` + strconv.Itoa(h) + `}}]}}}`
}

func TestHerdrSplitsBesideOrBelowAndRunsTheReview(t *testing.T) {
	argv := []string{"/bin/sift", "review", "--run", "r1"}
	for size, dir := range map[[2]int]string{{200, 50}: "right", {100, 60}: "down"} {
		path, log := fakeHerdr(t, layoutWith("p1", size[0], size[1]), "")
		h := herdr{path: path, pane: "p1"}
		if h.Name() != "herdr" {
			t.Fatalf("name = %q", h.Name())
		}
		if err := h.Open(context.Background(), argv); err != nil {
			t.Fatal(err)
		}
		want := []string{
			"pane layout --pane p1",
			"pane split --pane p1 --direction " + dir + " --focus",
			"pane run p9 /bin/sift review --run r1 && exit",
		}
		if got := herdrCalls(t, log); !slices.Equal(got, want) {
			t.Errorf("pane %v: calls = %q, want %q", size, got, want)
		}
	}
}

func TestHerdrFailures(t *testing.T) {
	path, log := fakeHerdr(t, layoutWith("someone-else", 200, 50), "")
	err := herdr{path: path, pane: "p1"}.Open(context.Background(), []string{"sift"})
	if err == nil || !strings.Contains(err.Error(), "does not list pane p1") {
		t.Fatalf("missing pane: err = %v", err)
	}
	if got := herdrCalls(t, log); len(got) != 1 {
		t.Fatalf("a failed layout must open nothing, calls = %q", got)
	}

	path, _ = fakeHerdr(t, "", `{"error":{"message":"pane p1 not found"}}`)
	err = herdr{path: path, pane: "p1"}.Open(context.Background(), []string{"sift"})
	if err == nil || !strings.Contains(err.Error(), "pane p1 not found") || strings.Contains(err.Error(), "{") {
		t.Fatalf("Herdr's JSON error should come through as its message: %v", err)
	}

	path, _ = fakeHerdr(t, "not json", "")
	if err := (herdr{path: path, pane: "p1"}).Open(context.Background(), []string{"sift"}); err == nil {
		t.Fatal("an unreadable layout should fail")
	}
}

func TestHerdrIsDetectedFirst(t *testing.T) {
	found := func(name string) (string, error) { return "/usr/bin/" + name, nil }
	vars := map[string]string{"HERDR_ENV": "1", "HERDR_PANE_ID": "p1", "TMUX": "/tmp/tmux-1/default,1,0"}
	get := func(k string) string { return vars[k] }
	if h, ok := detect(get, "linux", found); !ok || h.Name() != "herdr" || h.(herdr).pane != "p1" {
		t.Fatalf("detect = %v %v, want herdr ahead of tmux", h, ok)
	}
	vars["HERDR_ENV"] = "0"
	if h, _ := detect(get, "linux", found); h.Name() != "tmux" {
		t.Fatalf("without HERDR_ENV=1, detect = %v, want tmux", h.Name())
	}
}

// fakeHerdrSplit answers pane layout normally and pane split with splitOut and exit code, recording every call.
func fakeHerdrSplit(t *testing.T, splitOut string, splitExit int) (path, log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "calls")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + ShellQuote(log) + "\n" +
		"case \"$1 $2\" in\n" +
		"  'pane layout') printf '%s' " + ShellQuote(layoutWith("p1", 200, 50)) + ";;\n" +
		"  'pane split') printf '%s' " + ShellQuote(splitOut) + "; exit " + strconv.Itoa(splitExit) + ";;\n" +
		"esac\n"
	path = filepath.Join(dir, "herdr")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, log
}

func TestHerdrStopsWhenTheSplitFails(t *testing.T) {
	// A failed split prints its reason on stdout only, which the error carries.
	path, log := fakeHerdrSplit(t, "no room for a pane", 1)
	err := herdr{path: path, pane: "p1"}.Open(context.Background(), []string{"sift"})
	if err == nil || !strings.Contains(err.Error(), "no room for a pane") {
		t.Fatalf("err = %v, want the split's own message", err)
	}
	if got := herdrCalls(t, log); len(got) != 2 {
		t.Fatalf("nothing should run after a failed split, calls = %q", got)
	}

	path, log = fakeHerdrSplit(t, `{"result":{}}`, 0)
	err = herdr{path: path, pane: "p1"}.Open(context.Background(), []string{"sift"})
	if err == nil || !strings.Contains(err.Error(), "did not report its new pane") {
		t.Fatalf("err = %v, want a missing pane id refused", err)
	}
	if got := herdrCalls(t, log); len(got) != 2 {
		t.Fatalf("nothing should run without a pane id, calls = %q", got)
	}
}
