// Package cli is the sift command line: agent commands that print one JSON object, and the human's review.
package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/tyrantkhan/sift/internal/run"
	"github.com/tyrantkhan/sift/internal/tui"
)

// Version is this build's version. release-please bumps it with every plugin manifest, which a test keeps in
// step; release builds also set it with -ldflags.
var Version = "0.1.1" // x-release-please-version

type Env struct {
	Args   []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Getenv func(string) string
	Now    func() time.Time
	// Terminal reports whether stdin and stdout are an interactive terminal, and Review runs the review
	// interface; both default to the real ones and exist so tests can drive sift review.
	Terminal func() bool
	Review   func(root, id string, opts tui.Options) (published bool, err error)
}

type command struct {
	name    string
	who     string
	summary string
	help    string
	run     func(env *Env, args []string) error
}

func commands() []command {
	return []command{
		{"start", "agent", "Start a review round of plans, specs or code files", startHelp, cmdStart},
		{"add", "agent", "File findings into the round", addHelp, cmdAdd},
		{"summary", "agent", "Set the summary the human reads first", summaryHelp, cmdSummary},
		{"handoff", "agent", "Open the review for the human in a pane beside the agent", handoffHelp, cmdHandoff},
		{"wait", "agent", "Block until the human sends a finding back or publishes", waitHelp, cmdWait},
		{"reply", "agent", "Answer a finding the human sent back, optionally revising or withdrawing it", replyHelp, cmdReply},
		{"feedback", "agent", "Print the human's decisions", feedbackHelp, cmdFeedback},
		{"show", "anyone", "Show a run's findings and their state", showHelp, cmdShow},
		{"list", "anyone", "List every run", listHelp, cmdList},
		{"review", "human", "Decide each finding, one at a time, then publish", reviewHelp, cmdReview},
	}
}

// Main returns the process exit code.
func Main(env *Env) int {
	if len(env.Args) == 0 || env.Args[0] == "help" || env.Args[0] == "-h" || env.Args[0] == "--help" {
		if len(env.Args) > 1 {
			for _, c := range commands() {
				if c.name == env.Args[1] {
					fmt.Fprint(env.Stdout, c.help)
					return 0
				}
			}
		}
		usage(env.Stdout)
		return 0
	}
	name := env.Args[0]
	if name == "version" || name == "--version" {
		fmt.Fprintln(env.Stdout, "sift "+Version)
		return 0
	}
	for _, c := range commands() {
		if c.name != name {
			continue
		}
		err := c.run(env, env.Args[1:])
		if err == nil {
			return 0
		}
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		writeError(env, name, err)
		return 1
	}
	fmt.Fprintf(env.Stderr, "sift: unknown command %q\n\n", name)
	usage(env.Stderr)
	return 2
}

func usage(w io.Writer) {
	fmt.Fprint(w, `sift: decide an agent's findings on a plan, spec or code one at a time, then hand your decisions back.

Usage: sift <command> [flags]

`)
	for _, c := range commands() {
		fmt.Fprintf(w, "  %-9s %-7s %s\n", c.name, c.who, c.summary)
	}
	fmt.Fprint(w, `
Run sift help <command> for its flags, input and result, and sift --version for this build's version.

Environment:
  SIFT_HOME   where runs are stored (default $XDG_DATA_HOME/sift, else ~/.local/share/sift)
  SIFT_RUN    the run a command acts on when --run is not given (default: the latest run started)
`)
}

// writeError prints a refusal as JSON on stdout, the channel agents read, and anything else on stderr as well.
func writeError(env *Env, name string, err error) {
	var rf *run.Refusal
	if !errors.As(err, &rf) {
		rf = &run.Refusal{Code: "error", Message: err.Error()}
	}
	b, _ := json.Marshal(map[string]any{"ok": false, "command": name, "error": rf})
	fmt.Fprintln(env.Stdout, string(b))
	if rf.Code == "error" {
		fmt.Fprintln(env.Stderr, "sift "+name+": "+err.Error())
	}
}

func writeJSON(env *Env, name string, fields map[string]any) error {
	out := map[string]any{"ok": true, "command": name}
	for k, v := range fields {
		out[k] = v
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding result: %w", err)
	}
	_, err = fmt.Fprintln(env.Stdout, string(b))
	return err
}

// parse lets flags follow positional arguments, which the flag package alone stops at. "--" ends flags.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return pos, nil
		}
		// Parse consumes a "--" terminator itself, so it shows only as the argument just before what is left.
		if i := len(args) - len(rest); i > 0 && args[i-1] == "--" {
			return append(pos, rest...), nil
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}

func newFlags(env *Env, name, help string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	fs.Usage = func() { fmt.Fprint(env.Stderr, help) }
	return fs
}

func badArgs(name, format string, args ...any) error {
	return &run.Refusal{Code: "invalid-args", Message: fmt.Sprintf(format, args...), Fix: "run sift help " + name}
}

type target struct {
	root string
	id   string
}

func resolveTarget(env *Env, flagRun string) (target, error) {
	root, err := run.DataRoot(env.Getenv)
	if err != nil {
		return target{}, err
	}
	id, err := run.Resolve(root, flagRun, env.Getenv)
	if err != nil {
		return target{}, err
	}
	return target{root: root, id: id}, nil
}

func readInput(env *Env, from string) ([]byte, error) {
	if from == "" || from == "-" {
		return io.ReadAll(env.Stdin)
	}
	b, err := os.ReadFile(from)
	if err != nil {
		return nil, &run.Refusal{Code: "invalid-input", Message: fmt.Sprintf("reading %s: %v", from, err),
			Fix: "pass an existing file to --from, or pipe the input on stdin"}
	}
	return b, nil
}

// printable drops control and invisible formatting characters, so text from a run cannot drive the terminal it
// is printed to. Newlines and tabs stay.
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		if (unicode.IsControl(r) && r != '\n' && r != '\t') || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(printable(s)), " ")
	if r := []rune(s); len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}
