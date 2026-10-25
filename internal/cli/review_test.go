package cli

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tyrantkhan/sift/internal/run"
)

// reviewEnv runs sift review with a fake terminal and a fake review interface.
func reviewEnv(a *agent, out *bytes.Buffer, review func(root, id string) (bool, error), args ...string) *Env {
	return &Env{Args: args, Stdin: strings.NewReader(""), Stdout: out, Stderr: io.Discard,
		Getenv:   func(k string) string { return map[string]string{"SIFT_HOME": a.home}[k] },
		Now:      time.Now,
		Terminal: func() bool { return true },
		Review:   review}
}

func TestReviewHoldsTheSessionAndSaysWhereToGo(t *testing.T) {
	a, _ := newAgent(t)
	_, res := a.do("", "start", "plan.md", "--agent", "Codex")
	id := res["run"].(string)
	a.do(`{"title":"t","body":"b"}`, "add")

	var out bytes.Buffer
	review := func(root, rid string) (bool, error) {
		if open, _ := run.OpenSession(root, rid); !open {
			t.Error("the review session is not marked open while the interface runs")
		}
		// The human decides and sends from inside the interface.
		_, err := run.Update(root, rid, func(r *run.Run, _ run.Docs) error {
			if err := r.Decide("F1", nil, run.Approved, "", time.Now()); err != nil {
				return err
			}
			return r.Publish("", time.Now())
		})
		return err == nil, err
	}
	if code := Main(reviewEnv(a, &out, review, "review", "--run", id)); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	if out.String() != "Sent. Head back to Codex.\n" {
		t.Fatalf("output = %q", out.String())
	}
	if open, _ := run.OpenSession(a.home, id); open {
		t.Fatal("the session stayed marked open after the review")
	}
}

func TestReviewQuitWithoutSendingPrintsNothing(t *testing.T) {
	a, _ := newAgent(t)
	_, res := a.do("", "start", "plan.md")
	id := res["run"].(string)
	var out bytes.Buffer
	if code := Main(reviewEnv(a, &out, func(string, string) (bool, error) { return false, nil }, "review", "--run", id)); code != 0 || out.Len() != 0 {
		t.Fatalf("exit %d, output %q", code, out.String())
	}
}

func TestReviewRefusals(t *testing.T) {
	a, _ := newAgent(t)
	_, res := a.do("", "start", "plan.md")
	id := res["run"].(string)

	release, err := run.MarkSession(a.home, id)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	called := false
	Main(reviewEnv(a, &out, func(string, string) (bool, error) { called = true; return false, nil }, "review", "--run", id))
	if called || !strings.Contains(out.String(), `"review-open"`) {
		t.Fatalf("a second review should be refused: %s", out.String())
	}
	release()

	out.Reset()
	failing := func(string, string) (bool, error) { return false, errors.New("terminal went away") }
	if code := Main(reviewEnv(a, &out, failing, "review", "--run", id)); code != 1 || !strings.Contains(out.String(), "terminal went away") {
		t.Fatalf("a failed interface: exit %d %s", code, out.String())
	}

	out.Reset()
	env := reviewEnv(a, &out, failing, "review", "--run", id)
	env.Getenv = func(k string) string { return map[string]string{"SIFT_HOME": a.home, "TERM": "dumb"}[k] }
	if Main(env); !strings.Contains(out.String(), `"tty"`) {
		t.Fatalf("TERM=dumb should be refused: %s", out.String())
	}
}

// fakeTmux puts a tmux on PATH that reports a wide pane and records its calls, failing splits when fail is set.
func fakeTmux(t *testing.T, fail bool) (log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "calls")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\n" +
		"case \"$1\" in display-message) echo '200 50';; split-window) " + map[bool]string{true: "echo 'no space for new pane' >&2; exit 1", false: "true"}[fail] + ";; esac\n"
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func TestHandoffOpensThePaneInTmux(t *testing.T) {
	a, _ := newAgent(t)
	_, res := a.do("", "start", "plan.md")
	id := res["run"].(string)
	a.do(`{"title":"t","body":"b"}`, "add")
	inTmux := func(a *agent) func(string) string {
		return func(k string) string {
			return map[string]string{"SIFT_HOME": a.home, "TMUX": "/tmp/tmux-1/default,1,0", "TMUX_PANE": "%1"}[k]
		}
	}

	log := fakeTmux(t, false)
	var out bytes.Buffer
	env := &Env{Args: []string{"handoff"}, Stdin: strings.NewReader(""), Stdout: &out, Stderr: io.Discard, Getenv: inTmux(a), Now: time.Now}
	if code := Main(env); code != 0 || !strings.Contains(out.String(), `"host": "tmux"`) {
		t.Fatalf("handoff: exit %d %s", code, out.String())
	}
	calls, _ := os.ReadFile(log)
	if !strings.Contains(string(calls), "split-window -h -t %1") || !strings.Contains(string(calls), "review --run "+id) ||
		!strings.Contains(string(calls), "--host tmux") {
		t.Fatalf("tmux calls:\n%s", calls)
	}

	fakeTmux(t, true)
	out.Reset()
	if code := Main(env); code != 1 || !strings.Contains(out.String(), `"pane-failed"`) || !strings.Contains(out.String(), "no space for new pane") {
		t.Fatalf("failed split: exit %d %s", code, out.String())
	}
}

func TestStartRefusals(t *testing.T) {
	a, dir := newAgent(t)
	if _, res := a.do("", "start"); errCode(res) != "invalid-args" {
		t.Errorf("no files: %v", res)
	}
	if _, res := a.do("", "start", "plan.md", "--changed"); errCode(res) != "invalid-args" {
		t.Errorf("files and --changed: %v", res)
	}
	// The temp directory is not a git repository.
	if _, res := a.do("", "start", "--changed"); errCode(res) != "git" {
		t.Errorf("--changed outside git: %v", res)
	}
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("add", ".")
	git("commit", "-q", "-m", "init")
	if _, res := a.do("", "start", "--changed"); errCode(res) != "no-changes" {
		t.Errorf("clean repository: %v", res)
	}
	if _, res := a.do("", "start", "--changed", "--base", "no-such-branch"); errCode(res) != "git" {
		t.Errorf("unknown base: %v", res)
	}
}

func TestClearTopRowOnlyInOrca(t *testing.T) {
	inOrca := func(k string) string { return map[string]string{"ORCA_TERMINAL_HANDLE": "term-1"}[k] }
	none := func(string) string { return "" }
	for _, c := range []struct {
		host string
		env  func(string) string
		want bool
	}{
		{"orca", none, true},
		{"", inOrca, true},
		{"tmux", inOrca, false}, // handoff named another host, which draws nothing over the pane
		{"", none, false},
		{"iterm", none, false},
	} {
		if got := clearTopRow(c.host, c.env); got != c.want {
			t.Errorf("clearTopRow(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}
