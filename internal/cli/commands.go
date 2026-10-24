package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tyrantkhan/sift/internal/pane"
	"github.com/tyrantkhan/sift/internal/run"
)

const startHelp = `Usage: sift start <file>... [--title <text>] [--agent <name>]
       sift start --changed [--base <ref>] [--title <text>] [--agent <name>]

Start a review round of one or more files: a plan, a spec, code, or any mix. The files are snapshotted, so
finding anchors stay valid while you edit the originals. The new run becomes the latest, which later commands
act on when --run is not given.

--changed reviews the files changed in the current git repository since the point where HEAD left --base
(default HEAD itself): committed on the branch, staged, unstaged and untracked. Deleted, empty and binary files
are left out and listed in "skipped". --base takes one branch, tag or commit, not a range.

--agent is the name the human knows you by, such as Claude or Codex; the review shows it wherever it refers
to you. Without it, the review says "the agent".

Paths are shown relative to the current directory, or with --changed to the repository root. Use them as
anchor.path in sift add.

Result:
  {"ok": true, "command": "start", "run": "<id>",
   "files": [{"path": "docs/plan.md", "lines": 120, "headings": [{"level": 2, "line": 5, "text": "Data model"}]},
             {"path": "internal/store.go", "lines": 300}],
   "skipped": [{"path": "logo.png", "reason": "empty or binary"}]}
`

func cmdStart(env *Env, args []string) error {
	fs := newFlags(env, "start", startHelp)
	title := fs.String("title", "", `title shown to the human (default: "<first file> and N more", or with --changed "Changes since <base>")`)
	agent := fs.String("agent", "", "your name as the human knows it, such as Claude or Codex")
	changed := fs.Bool("changed", false, "review the files changed in git instead of naming them")
	base := fs.String("base", "HEAD", "with --changed, the branch, tag or commit the work started from")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("reading the working directory: %w", err)
	}
	var skipped []map[string]string
	paths := pos
	if *changed {
		if len(pos) > 0 {
			return badArgs("start", "pass files or --changed, not both")
		}
		// Changed files are named from the repository root, as git names them.
		if paths, skipped, cwd, err = changedFiles(cwd, *base); err != nil {
			return err
		}
		if *title == "" {
			*title = "Changes since " + *base
		}
	} else if len(pos) == 0 {
		return badArgs("start", "pass the files to review, or --changed")
	}
	root, err := run.DataRoot(env.Getenv)
	if err != nil {
		return err
	}
	r, err := run.Create(root, run.Start{Paths: paths, Cwd: cwd, Title: *title, Agent: *agent}, env.Now())
	if err != nil {
		return err
	}
	_, docs, err := run.Load(root, r.ID)
	if err != nil {
		return err
	}
	files := make([]map[string]any, len(r.Files))
	for i, f := range r.Files {
		files[i] = map[string]any{"path": f.Path, "lines": f.Lines}
		if hs := docs[f.Path].Headings(); len(hs) > 0 {
			files[i]["headings"] = hs
		}
	}
	out := map[string]any{"run": r.ID, "files": files}
	if len(skipped) > 0 {
		out["skipped"] = skipped
	}
	return writeJSON(env, "start", out)
}

// changedFiles lists the files changed since the merge base of base and HEAD (staged, unstaged or untracked) as
// absolute paths, and the repository root. Files left out (deleted, empty or binary) are returned separately so the
// agent knows about them.
func changedFiles(cwd, base string) ([]string, []map[string]string, string, error) {
	if strings.HasPrefix(base, "-") || strings.Contains(base, "..") {
		return nil, nil, "", &run.Refusal{Code: "invalid-args", Message: fmt.Sprintf("--base %q is not a single ref", base),
			Fix: "pass a branch, tag or commit, such as --base main"}
	}
	dir := cwd
	gitOut := func(args ...string) ([]byte, error) {
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		var stderr bytes.Buffer
		c.Stderr = &stderr
		out, err := c.Output()
		if err != nil {
			reason := strings.TrimSpace(stderr.String())
			if reason == "" {
				reason = err.Error()
			}
			return nil, &run.Refusal{Code: "git", Message: fmt.Sprintf("git %s: %s", strings.Join(args, " "), reason),
				Fix: "run sift start --changed inside a git repository, with a valid --base"}
		}
		return out, nil
	}
	top, err := gitOut("rev-parse", "--show-toplevel")
	if err != nil {
		return nil, nil, "", err
	}
	// From the root, git names every path from the root, whatever diff.relative says.
	toplevel := strings.TrimSpace(string(top))
	dir = toplevel
	var diff []byte
	if base == "HEAD" && exec.Command("git", "-C", dir, "rev-parse", "--verify", "--quiet", "HEAD").Run() != nil {
		// A repository with no commits has no HEAD to compare against; everything staged or edited is new.
		staged, err := gitOut("diff", "--name-only", "-z", "--cached", "--")
		if err != nil {
			return nil, nil, "", err
		}
		edited, err := gitOut("diff", "--name-only", "-z", "--")
		if err != nil {
			return nil, nil, "", err
		}
		diff = slices.Concat(staged, edited)
	} else if diff, err = gitOut("diff", "--name-only", "-z", "--merge-base", "--end-of-options", base, "--"); err != nil {
		// The merge base makes --base main mean "this branch's work", not "everything that differs from main".
		return nil, nil, "", err
	}
	untracked, err := gitOut("ls-files", "--others", "--exclude-standard", "-z", "--full-name", ":/")
	if err != nil {
		return nil, nil, "", err
	}
	var paths []string
	var skipped []map[string]string
	seen := map[string]bool{}
	for _, name := range strings.Split(string(diff)+string(untracked), "\x00") {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		abs := filepath.Join(toplevel, name)
		b, err := os.ReadFile(abs)
		switch {
		case errors.Is(err, os.ErrNotExist):
			skipped = append(skipped, map[string]string{"path": name, "reason": "deleted"})
		case err != nil:
			skipped = append(skipped, map[string]string{"path": name, "reason": err.Error()})
		case !run.Reviewable(b):
			skipped = append(skipped, map[string]string{"path": name, "reason": "empty or binary"})
		default:
			paths = append(paths, abs)
		}
	}
	if len(paths) == 0 {
		return nil, nil, "", &run.Refusal{Code: "no-changes", Message: "git reports no reviewable changes against " + base,
			Fix: "pass the files to review, or a different --base"}
	}
	return paths, skipped, toplevel, nil
}

const addHelp = `Usage: sift add [--from <file.json>] [--run <id>]

File findings from --from, or stdin. The input is one object or an array. A batch is stored entirely or not
at all; a refusal names the failing entry.

  {
    "title":    "One line, at most 120 characters (required)",
    "body":     "Markdown: what is wrong or missing and why it matters (required)",
    "proposal": "Markdown: the change you will make if the human approves",
    "severity": "critical | major | minor | trivial",
    "label":    "one word, such as bug, risk, gap, question, simplification, sequencing",
    "anchor":   {"path": "docs/plan.md", "heading": "Data model"}
                or {"path": "internal/store.go", "startLine": 40, "endLine": 52}
  }

anchor.path is a path as sift start printed it, or one relative to your directory; a path that could mean
two files is refused. It MAY be left out when the run has one file. Omit anchor for a finding about the work
as a whole. Lines refer to the snapshot that start took.

A heading works only in a Markdown file and covers its whole section. It matches exactly (case-insensitive),
else as the only heading containing the text; more than one candidate is refused with their line numbers, so
anchor by lines then. Unknown fields are refused.

Result:
  {"ok": true, "command": "add", "run": "<id>", "added": ["F1", "F2"], "counts": {...}}
`

func cmdAdd(env *Env, args []string) error {
	fs := newFlags(env, "add", addHelp)
	from := fs.String("from", "", "JSON file of findings (default: stdin)")
	runFlag := fs.String("run", "", "run id")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	t, err := resolveTarget(env, *runFlag)
	if err != nil {
		return err
	}
	b, err := readInput(env, *from)
	if err != nil {
		return err
	}
	in, err := decodeFindings(b)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("reading the working directory: %w", err)
	}
	var ids []string
	r, err := run.Update(t.root, t.id, func(r *run.Run, p run.Docs) error {
		ids, err = r.Add(p, cwd, in)
		return err
	})
	if err != nil {
		return err
	}
	return writeJSON(env, "add", map[string]any{"run": r.ID, "added": ids, "counts": r.Counts()})
}

func decodeFindings(b []byte) ([]run.NewFinding, error) {
	b = bytes.TrimSpace(b)
	invalid := func(err error) error {
		return &run.Refusal{Code: "invalid-input", Message: "parsing findings: " + err.Error(), Fix: "run sift help add for the shape"}
	}
	var in []run.NewFinding
	if len(b) > 0 && b[0] == '{' {
		var one run.NewFinding
		if err := strictDecode(b, &one); err != nil {
			return nil, invalid(err)
		}
		return []run.NewFinding{one}, nil
	}
	if err := strictDecode(b, &in); err != nil {
		return nil, invalid(err)
	}
	return in, nil
}

func strictDecode(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if d.More() {
		return fmt.Errorf("unexpected data after the JSON value")
	}
	return nil
}

const summaryHelp = `Usage: sift summary [--text <markdown>] [--run <id>]

Set the summary the human reads before the first finding: what you reviewed, the shape of what you found,
and anything you could not check. Reads stdin when --text is not given. Replaces any earlier summary.

Result:
  {"ok": true, "command": "summary", "run": "<id>"}
`

func cmdSummary(env *Env, args []string) error {
	fs := newFlags(env, "summary", summaryHelp)
	text := fs.String("text", "", "summary text (default: stdin)")
	runFlag := fs.String("run", "", "run id")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	t, err := resolveTarget(env, *runFlag)
	if err != nil {
		return err
	}
	s := *text
	if s == "" {
		b, err := readInput(env, "")
		if err != nil {
			return err
		}
		s = string(b)
	}
	if strings.TrimSpace(s) == "" {
		return badArgs("summary", "the summary is empty")
	}
	r, err := run.Update(t.root, t.id, func(r *run.Run, _ run.Docs) error { return r.SetSummary(s) })
	if err != nil {
		return err
	}
	return writeJSON(env, "summary", map[string]any{"run": r.ID})
}

const handoffHelp = `Usage: sift handoff [--run <id>]

Open sift review for the human in a new pane beside the agent's, then return. Hosts, first detected wins:
  tmux    TMUX set and tmux on PATH
  orca    ORCA_TERMINAL_HANDLE set and orca on PATH
  iterm   TERM_PROGRAM=iTerm.app on macOS (splits the agent's own session)

Refusals: review-open when a review of this run is already open (the human is on it; run sift wait),
no-pane-host and pane-failed (ask the human to run the command in error.fix in another terminal).

Result:
  {"ok": true, "command": "handoff", "run": "<id>", "host": "iterm"}
`

func cmdHandoff(env *Env, args []string) error {
	fs := newFlags(env, "handoff", handoffHelp)
	runFlag := fs.String("run", "", "run id")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	t, err := resolveTarget(env, *runFlag)
	if err != nil {
		return err
	}
	r, _, err := run.Load(t.root, t.id)
	if err != nil {
		return err
	}
	if r.Published != nil {
		return &run.Refusal{Code: "published", Message: "run " + r.ID + " was already published", Fix: "run sift feedback"}
	}
	if len(r.Findings) == 0 {
		return &run.Refusal{Code: "no-findings", Message: "run " + r.ID + " has no findings", Fix: "file findings with sift add first"}
	}
	open, err := run.OpenSession(t.root, t.id)
	if err != nil {
		return err
	}
	if open {
		return &run.Refusal{Code: "review-open", Message: "sift review is already open for " + t.id,
			Fix: "tell the human their review is open, then run sift wait --run " + t.id}
	}
	argv, err := reviewArgv(t)
	if err != nil {
		return err
	}
	fix := "ask the human to run this in another terminal: " + strings.Join(quoteAll(argv), " ")
	host, ok := pane.Detect(env.Getenv)
	if !ok {
		return &run.Refusal{Code: "no-pane-host", Message: "no supported terminal host (tmux, orca, iTerm2) detected", Fix: fix}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := host.Open(ctx, argv); err != nil {
		return &run.Refusal{Code: "pane-failed", Message: host.Name() + ": " + err.Error(), Fix: fix}
	}
	return writeJSON(env, "handoff", map[string]any{"run": t.id, "host": host.Name()})
}

// reviewArgv is the command that opens this run's review. The human's shell does not inherit the agent's
// environment, so the data root rides on the command line.
func reviewArgv(t target) ([]string, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locating the sift executable: %w", err)
	}
	return []string{exe, "review", "--run", t.id, "--home", t.root}, nil
}

func quoteAll(argv []string) []string {
	q := make([]string, len(argv))
	for i, a := range argv {
		q[i] = pane.ShellQuote(a)
	}
	return q
}

const waitHelp = `Usage: sift wait [--run <id>] [--timeout 9m]

Block until something needs the agent, then print one event:
  published  the human published; the result carries the full feedback (same fields as sift feedback)
  discuss    the human sent findings back; answer each with sift reply, then wait again
  timeout    nothing yet. With reviewOpen true, wait again. With reviewOpen false nobody is reviewing:
             run sift handoff again, or ask the human to run reviewCommand in a terminal

If your shell tool caps how long one command runs, pass --timeout below that cap.

Result:
  {"ok": true, "command": "wait", "run": "<id>", "event": "discuss",
   "findings": [{"id": "F3", "title": "...", "thread": [{"author": "human", "text": "..."}]}]}
`

func cmdWait(env *Env, args []string) error {
	fs := newFlags(env, "wait", waitHelp)
	runFlag := fs.String("run", "", "run id")
	timeout := fs.Duration("timeout", 9*time.Minute, "how long to wait before reporting timeout")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	t, err := resolveTarget(env, *runFlag)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(*timeout)
	for {
		r, err := run.LoadRun(t.root, t.id)
		if err != nil {
			return err
		}
		if r.Published != nil {
			fields := ledger(r)
			fields["event"] = "published"
			return writeJSON(env, "wait", fields)
		}
		var sent []map[string]any
		for _, f := range r.Findings {
			if f.Status == run.Discuss {
				sent = append(sent, map[string]any{"id": f.ID, "title": f.Title, "body": f.Body,
					"proposal": f.Proposal, "anchor": f.Anchor, "thread": f.Thread})
			}
		}
		if len(sent) > 0 {
			return writeJSON(env, "wait", map[string]any{"run": r.ID, "event": "discuss", "findings": sent})
		}
		if time.Now().After(deadline) {
			open, err := run.OpenSession(t.root, t.id)
			if err != nil {
				return err
			}
			argv, err := reviewArgv(t)
			if err != nil {
				return err
			}
			return writeJSON(env, "wait", map[string]any{"run": r.ID, "event": "timeout", "reviewOpen": open,
				"reviewCommand": strings.Join(quoteAll(argv), " "), "counts": r.Counts()})
		}
		time.Sleep(500 * time.Millisecond)
	}
}

const replyHelp = `Usage: sift reply <finding-id> --text <answer> [--title ..] [--body ..] [--proposal ..]
                  [--severity ..] [--withdraw] [--run <id>]

Answer a finding the human sent back. Revise it with the field flags, or --withdraw it when the human's note
shows it is wrong. A revised or answered finding returns to the human as pending, to decide again.

Result:
  {"ok": true, "command": "reply", "run": "<id>", "finding": {...}}
`

func cmdReply(env *Env, args []string) error {
	fs := newFlags(env, "reply", replyHelp)
	runFlag := fs.String("run", "", "run id")
	text := fs.String("text", "", "your answer to the human's note (required)")
	withdraw := fs.Bool("withdraw", false, "withdraw the finding")
	var rev run.Revision
	optional := func(name, usage string, dst **string) {
		fs.Func(name, usage, func(v string) error { *dst = &v; return nil })
	}
	optional("title", "revised title", &rev.Title)
	optional("body", "revised body", &rev.Body)
	optional("proposal", "revised proposal", &rev.Proposal)
	optional("severity", "revised severity", &rev.Severity)
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return badArgs("reply", "reply takes exactly one finding id")
	}
	t, err := resolveTarget(env, *runFlag)
	if err != nil {
		return err
	}
	r, err := run.Update(t.root, t.id, func(r *run.Run, _ run.Docs) error {
		return r.Reply(pos[0], *text, rev, *withdraw, env.Now())
	})
	if err != nil {
		return err
	}
	f, _ := r.Finding(pos[0])
	return writeJSON(env, "reply", map[string]any{"run": r.ID, "finding": f})
}

const feedbackHelp = `Usage: sift feedback [--run <id>]

Print the human's decisions. Once published, act on them:
  apply             approved as proposed: make the proposal's change
  applyWithChanges  approved with the human's suggestion: make the change, shaped by "suggestion"
  rejected          do not make the change; "reason" says why when the human gave one
  withdrawn         you withdrew it during discussion; nothing to do
  note              the human's closing note on the work as a whole, when given
An entry carries "thread" when the human discussed it with you; what you agreed there counts.
Anchors are line numbers in the snapshot taken at start; earlier edits shift them, so locate each change by
heading or content, or apply from the bottom of the file up.
Before publishing, "open" lists findings still pending or in discussion.

Result:
  {"ok": true, "command": "feedback", "run": "<id>", "files": [{"path": "...", "abs": "..."}], "published": true,
   "apply": [{"id": "F1", "title": "...", "proposal": "...", "anchor": {...}, "file": "/abs/path"}], ...}

An anchored entry's "file" is the absolute path of the original file to edit.
`

func cmdFeedback(env *Env, args []string) error {
	fs := newFlags(env, "feedback", feedbackHelp)
	runFlag := fs.String("run", "", "run id")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	t, err := resolveTarget(env, *runFlag)
	if err != nil {
		return err
	}
	r, _, err := run.Load(t.root, t.id)
	if err != nil {
		return err
	}
	return writeJSON(env, "feedback", ledger(r))
}

func fileList(r *run.Run) []map[string]string {
	out := make([]map[string]string, len(r.Files))
	for i, f := range r.Files {
		out[i] = map[string]string{"path": f.Path, "abs": f.Abs}
	}
	return out
}

func ledger(r *run.Run) map[string]any {
	apply, withChanges, rejected, withdrawn, open := []any{}, []any{}, []any{}, []any{}, []any{}
	for _, f := range r.Findings {
		base := map[string]any{"id": f.ID, "title": f.Title}
		if f.Anchor != nil {
			base["anchor"] = f.Anchor
			for _, file := range r.Files {
				if file.Path == f.Anchor.Path {
					base["file"] = file.Abs
				}
			}
		}
		// The discussion can settle details the proposal does not, so the agent reads it when applying.
		if len(f.Thread) > 0 && f.Status != run.Withdrawn {
			base["thread"] = f.Thread
		}
		switch f.Status {
		case run.Approved:
			base["proposal"] = f.Proposal
			if f.Proposal == "" {
				base["body"] = f.Body
			}
			apply = append(apply, base)
		case run.Amended:
			base["proposal"] = f.Proposal
			base["body"] = f.Body
			base["suggestion"] = f.Note
			withChanges = append(withChanges, base)
		case run.Rejected:
			if f.Note != "" {
				base["reason"] = f.Note
			}
			rejected = append(rejected, base)
		case run.Withdrawn:
			withdrawn = append(withdrawn, base)
		default:
			base["status"] = f.Status
			open = append(open, base)
		}
	}
	out := map[string]any{
		"run": r.ID, "files": fileList(r), "published": r.Published != nil, "counts": r.Counts(),
		"apply": apply, "applyWithChanges": withChanges, "rejected": rejected, "withdrawn": withdrawn,
	}
	if r.Published != nil {
		out["publishedAt"] = r.Published.At
		if r.Published.Note != "" {
			out["note"] = r.Published.Note
		}
	} else {
		out["open"] = open
	}
	return out
}

const showHelp = `Usage: sift show [--run <id>] [--json]

Show a run's summary and every finding with its state.
`

func cmdShow(env *Env, args []string) error {
	fs := newFlags(env, "show", showHelp)
	runFlag := fs.String("run", "", "run id")
	asJSON := fs.Bool("json", false, "print the whole run as JSON")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	t, err := resolveTarget(env, *runFlag)
	if err != nil {
		return err
	}
	r, _, err := run.Load(t.root, t.id)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(env, "show", map[string]any{"run": r})
	}
	c := r.Counts()
	state := "in review"
	if r.Published != nil {
		state = "published " + r.Published.At.Local().Format("2006-01-02 15:04")
	}
	fmt.Fprintf(env.Stdout, "%s  %s\n%s\n", r.ID, state, printable(r.Title))
	for _, f := range r.Files {
		fmt.Fprintf(env.Stdout, "  %s\n", printable(f.Path))
	}
	fmt.Fprintf(env.Stdout, "%d/%d decided, %d pending, %d in discussion\n\n", c.Decided(), c.Total, c.Pending, c.Discuss)
	if r.Summary != "" {
		fmt.Fprintf(env.Stdout, "%s\n\n", printable(r.Summary))
	}
	for _, f := range run.Ordered(r.Findings) {
		sev := f.Severity
		if sev == "" {
			sev = "-"
		}
		fmt.Fprintf(env.Stdout, "%-4s %-9s %-8s %s\n", f.ID, f.Status, sev, oneLine(f.Title, 90))
	}
	return nil
}

const listHelp = `Usage: sift list [--json]

List every run, newest first.
`

func cmdList(env *Env, args []string) error {
	fs := newFlags(env, "list", listHelp)
	asJSON := fs.Bool("json", false, "print as JSON")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	root, err := run.DataRoot(env.Getenv)
	if err != nil {
		return err
	}
	runs, skipped, err := run.List(root)
	if err != nil {
		return err
	}
	if *asJSON {
		items := make([]map[string]any, 0, len(runs))
		for _, r := range runs {
			items = append(items, map[string]any{"run": r.ID, "title": r.Title, "files": len(r.Files),
				"published": r.Published != nil, "counts": r.Counts()})
		}
		return writeJSON(env, "list", map[string]any{"runs": items, "skipped": skipped})
	}
	for _, sk := range skipped {
		fmt.Fprintf(env.Stderr, "sift list: skipped %s: %s\n", sk.ID, printable(sk.Reason))
	}
	if len(runs) == 0 {
		fmt.Fprintln(env.Stdout, "No runs yet. An agent starts one with sift start <files>.")
		return nil
	}
	for _, r := range runs {
		c := r.Counts()
		state := "in review"
		if r.Published != nil {
			state = "published"
		}
		fmt.Fprintf(env.Stdout, "%-50s %-10s %3d/%-3d %s\n", r.ID, state, c.Decided(), c.Total, oneLine(r.Title, 40))
	}
	return nil
}
