// Package pane opens the review interface in a terminal pane beside the agent's.
package pane

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/term"
)

type Host interface {
	Name() string
	Open(ctx context.Context, argv []string) error
}

// Detect picks the first host whose environment says the agent runs inside it.
func Detect(getenv func(string) string) (Host, bool) {
	return detect(getenv, runtime.GOOS, exec.LookPath)
}

func detect(getenv func(string) string, goos string, lookPath func(string) (string, error)) (Host, bool) {
	// Herdr sets these in the panes it manages. It comes first: a Herdr pane may run inside tmux.
	if pane := getenv("HERDR_PANE_ID"); getenv("HERDR_ENV") == "1" && pane != "" {
		if p, err := lookPath("herdr"); err == nil {
			return herdr{path: p, pane: pane}, true
		}
	}
	if getenv("TMUX") != "" {
		if p, err := lookPath("tmux"); err == nil {
			return tmux{path: p, pane: getenv("TMUX_PANE")}, true
		}
	}
	if h := getenv("ORCA_TERMINAL_HANDLE"); h != "" {
		if p, err := lookPath("orca"); err == nil {
			return orca{path: p, handle: h}, true
		}
	}
	// Without the agent's session id, iTerm could only split whatever window has focus, which may not be the
	// agent's, so it counts as unavailable and the human gets the command to run instead.
	if session := sessionUUID(getenv("ITERM_SESSION_ID")); goos == "darwin" && getenv("TERM_PROGRAM") == "iTerm.app" && session != "" {
		if p, err := lookPath("osascript"); err == nil {
			return iterm{path: p, session: session}, true
		}
	}
	return nil, false
}

// The review's smallest usable size, and the width from which the agent and the review sit side by side.
const (
	minCols    = 50
	minRows    = 14
	roomyWidth = 120
)

// below reports whether to split the agent's w×h pane below rather than beside, so the review gets at least its
// minimum size where either split allows it. Side by side wins in a wide pane. An unknown size (0×0) splits below,
// which at least keeps the full width.
func below(w, h int) bool {
	if w == 0 && h == 0 {
		return true
	}
	besideFits := (w-1)/2 >= minCols && h >= minRows
	belowFits := w >= minCols && (h-1)/2 >= minRows
	switch {
	case w >= roomyWidth && besideFits:
		return false
	case belowFits:
		return true
	case besideFits:
		return false
	}
	return w < roomyWidth
}

// ttySize is the size of the controlling terminal, or 0×0 when there is none, as in an agent's shell tool.
func ttySize() (int, int) {
	f, err := os.Open("/dev/tty")
	if err != nil {
		return 0, 0
	}
	// Opened read-only to read its size, so closing it cannot lose anything.
	defer func() { _ = f.Close() }()
	w, h, err := term.GetSize(f.Fd())
	if err != nil {
		return 0, 0
	}
	return w, h
}

// ShellQuote quotes s for a POSIX shell.
func ShellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && !strings.ContainsRune("-_./:=@", r)
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func shellLine(argv []string) string {
	q := make([]string, len(argv))
	for i, a := range argv {
		q[i] = ShellQuote(a)
	}
	return strings.Join(q, " ")
}

// runCmd runs a host's command and returns what it printed. A failure carries the host's own message: the error
// field of a JSON reply on stderr when there is one, as Herdr prints, else stderr or stdout as printed.
func runCmd(ctx context.Context, name string, args ...string) ([]byte, error) {
	var stderr, stdout bytes.Buffer
	c := exec.CommandContext(ctx, name, args...)
	c.Stdout, c.Stderr = &stdout, &stderr
	if err := c.Run(); err != nil {
		var reply struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		msg := strings.TrimSpace(stderr.String())
		if json.Unmarshal(stderr.Bytes(), &reply) == nil && reply.Error.Message != "" {
			msg = reply.Error.Message
		}
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		return nil, fmt.Errorf("%s: %w: %s", name, err, msg)
	}
	return stdout.Bytes(), nil
}

type tmux struct{ path, pane string }

func (tmux) Name() string { return "tmux" }

func (t tmux) Open(ctx context.Context, argv []string) error {
	args := []string{"split-window", "-v"}
	if !below(t.paneSize(ctx)) {
		args[1] = "-h"
	}
	if t.pane != "" {
		args = append(args, "-t", t.pane)
	}
	_, err := runCmd(ctx, t.path, append(args, shellLine(argv))...)
	return err
}

// paneSize asks tmux for the agent's pane size, or 0×0 when it cannot say.
func (t tmux) paneSize(ctx context.Context) (int, int) {
	args := []string{"display-message", "-p"}
	if t.pane != "" {
		args = append(args, "-t", t.pane)
	}
	out, err := exec.CommandContext(ctx, t.path, append(args, "#{pane_width} #{pane_height}")...).Output()
	if err != nil {
		return 0, 0
	}
	f := strings.Fields(string(out))
	if len(f) != 2 {
		return 0, 0
	}
	w, err1 := strconv.Atoi(f[0])
	h, err2 := strconv.Atoi(f[1])
	if err1 != nil || err2 != nil {
		return 0, 0
	}
	return w, h
}

type orca struct{ path, handle string }

func (orca) Name() string { return "orca" }

// Orca names the divider, not the placement: a vertical divider puts the new pane to the right. It reports no
// pane size, so the terminal's width decides.
func (o orca) Open(ctx context.Context, argv []string) error {
	divider := "horizontal"
	if !below(ttySize()) {
		divider = "vertical"
	}
	_, err := runCmd(ctx, o.path, "terminal", "split", "--terminal", o.handle, "--direction", divider,
		"--command", shellLine(argv), "--json")
	return err
}

type iterm struct{ path, session string }

func (iterm) Name() string { return "iterm" }

// sessionUUID takes the UUID from ITERM_SESSION_ID ("w0t1p0:UUID"), which iTerm's AppleScript calls the unique id.
func sessionUUID(v string) string {
	if i := strings.LastIndex(v, ":"); i >= 0 {
		return v[i+1:]
	}
	return ""
}

func appleString(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}

// Open splits the agent's own session by its id, so the pane lands beside the agent even if the human has
// focused another window.
func (i iterm) Open(ctx context.Context, argv []string) error {
	cmd := appleString(shellLine(argv))
	script := `tell application "iTerm2"
	repeat with w in windows
		repeat with t in tabs of w
			repeat with s in sessions of t
				if unique id of s is ` + appleString(i.session) + ` then
					tell s to split vertically with default profile command ` + cmd + `
					return
				end if
			end repeat
		end repeat
	end repeat
	error "agent session not found"
end tell`
	_, err := runCmd(ctx, i.path, "-e", script)
	return err
}
