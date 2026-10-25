package cli

import (
	"fmt"
	"os"

	"github.com/tyrantkhan/sift/internal/run"
	"github.com/tyrantkhan/sift/internal/tui"
)

const reviewHelp = `Usage: sift review [--run <id>] [--home <dir>] [--host <name>]

Decide each finding, one at a time, then send your decisions back to the agent. Human only: agents MUST
NOT run this; sift handoff opens it for the human.

Keys on a finding:
  a  approve           the agent makes its proposed change
  s  suggest           approve with your tweak; the agent applies it
  x  reject            drop it (X to give a reason)
  d  discuss           send it back to the agent with a question; you decide again after it answers
  u  undo              back to pending, or reinstate a finding the agent withdrew
  f  finish            send your decisions to the agent, or see what is left
  ← →  previous / next   l  list   ↑ ↓  scroll   ?  help   q  quit
`

func cmdReview(env *Env, args []string) error {
	fs := newFlags(env, "review", reviewHelp)
	runFlag := fs.String("run", "", "run id")
	home := fs.String("home", "", "data root, overriding SIFT_HOME")
	host := fs.String("host", "", "the terminal host that opened this pane, as sift handoff passes it")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	if *home != "" {
		base := env.Getenv
		env.Getenv = func(k string) string {
			if k == "SIFT_HOME" {
				return *home
			}
			return base(k)
		}
	}
	t, err := resolveTarget(env, *runFlag)
	if err != nil {
		return err
	}
	terminal, review := env.Terminal, env.Review
	if terminal == nil {
		terminal = func() bool { return isTerminal(os.Stdin) && isTerminal(os.Stdout) }
	}
	if review == nil {
		review = tui.Run
	}
	if !terminal() || env.Getenv("TERM") == "dumb" {
		return &run.Refusal{Code: "tty", Message: "sift review needs an interactive terminal",
			Fix: "the human runs sift review --run " + t.id + " in their own terminal"}
	}
	release, err := run.MarkSession(t.root, t.id)
	if err != nil {
		return err
	}
	defer release()
	published, err := review(t.root, t.id, tui.Options{ClearTopRow: clearTopRow(*host, env.Getenv)})
	if err != nil {
		return err
	}
	// Seen only when the review ran in a terminal that outlives it; a pane closes on exit.
	if published {
		r, _, err := run.Load(t.root, t.id)
		if err != nil {
			return err
		}
		fmt.Fprintf(env.Stdout, "Sent. Head back to %s.\n", r.AgentName())
	}
	return nil
}

// clearTopRow reports whether the review runs in Orca, which draws pane controls over a split pane's top-right
// corner. handoff names the host; a review the human opened by hand inside Orca is recognized by the handle
// Orca sets.
func clearTopRow(host string, getenv func(string) string) bool {
	return host == "orca" || (host == "" && getenv("ORCA_TERMINAL_HANDLE") != "")
}

func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}
