package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tyrantkhan/sift/internal/run"
)

// agent runs sift commands against one data root from one working directory, as an agent's shell would.
type agent struct {
	t    *testing.T
	home string
}

func newAgent(t *testing.T) (*agent, string) {
	t.Helper()
	dir := t.TempDir()
	plan := "# Plan\n\n## Rollout\nShip on Friday.\n\n## Data\nNo soft deletes.\n"
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte(plan), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	return &agent{t: t, home: t.TempDir()}, dir
}

// do runs a command with stdin and returns its exit code and the one JSON object it printed.
func (a *agent) do(stdin string, args ...string) (int, map[string]any) {
	a.t.Helper()
	code, out := a.raw(stdin, args...)
	var res map[string]any
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		a.t.Fatalf("sift %v printed no single JSON object: %v\n%s", args, err, out)
	}
	return code, res
}

func (a *agent) raw(stdin string, args ...string) (int, string) {
	a.t.Helper()
	var out bytes.Buffer
	env := &Env{Args: args, Stdin: strings.NewReader(stdin), Stdout: &out, Stderr: io.Discard,
		Getenv: func(k string) string {
			if k == "SIFT_HOME" {
				return a.home
			}
			return ""
		}, Now: time.Now}
	return Main(env), out.String()
}

// decide changes a run the way the human's review interface does.
func (a *agent) decide(id string, fn func(*run.Run) error) {
	a.t.Helper()
	if _, err := run.Update(a.home, id, func(r *run.Run, _ run.Docs) error { return fn(r) }); err != nil {
		a.t.Fatal(err)
	}
}

func errCode(res map[string]any) string {
	if e, ok := res["error"].(map[string]any); ok {
		return e["code"].(string)
	}
	return ""
}

func TestAgentDrivesAWholeRound(t *testing.T) {
	a, dir := newAgent(t)

	_, res := a.do("", "start", "plan.md", "--agent", "Codex", "--title", "Plan review")
	id := res["run"].(string)
	if res["ok"] != true || id == "" {
		t.Fatalf("start: %v", res)
	}

	_, res = a.do(`[{"title":"Friday cutover has no rollback","body":"b","proposal":"Use a flag","severity":"critical","anchor":{"heading":"Rollout"}},
		{"title":"Hard deletes lose history","body":"b","proposal":"Add deleted_at","severity":"major","anchor":{"startLine":7}}]`, "add")
	if res["ok"] != true || len(res["added"].([]any)) != 2 {
		t.Fatalf("add: %v", res)
	}

	if _, res = a.do("", "summary", "--text", "Two risks."); res["ok"] != true {
		t.Fatalf("summary --text: %v", res)
	}
	if _, res = a.do("Rollout and data.", "summary"); res["ok"] != true {
		t.Fatalf("summary from stdin: %v", res)
	}
	if code, res := a.do("  ", "summary"); code != 1 || errCode(res) != "invalid-args" {
		t.Fatalf("empty summary: exit %d %v", code, res)
	}

	_, show := a.raw("", "show")
	for _, want := range []string{id, "Plan review", "plan.md", "Rollout and data.", "F1", "critical", "Friday cutover"} {
		if !strings.Contains(show, want) {
			t.Fatalf("show lacks %q:\n%s", want, show)
		}
	}
	if _, res = a.do("", "show", "--json"); res["run"].(map[string]any)["id"] != id {
		t.Fatalf("show --json: %v", res)
	}
	if _, list := a.raw("", "list"); !strings.Contains(list, id) || !strings.Contains(list, "in review") {
		t.Fatalf("list:\n%s", list)
	}
	if _, res = a.do("", "list", "--json"); len(res["runs"].([]any)) != 1 {
		t.Fatalf("list --json: %v", res)
	}

	// Nothing for the agent yet: wait times out and says how the human opens the review.
	_, res = a.do("", "wait", "--timeout", "10ms")
	if res["event"] != "timeout" || res["reviewOpen"] != false || !strings.Contains(res["reviewCommand"].(string), "review --run "+id) {
		t.Fatalf("wait timeout: %v", res)
	}

	a.decide(id, func(r *run.Run) error {
		if err := r.Decide("F1", nil, run.Approved, "", time.Now()); err != nil {
			return err
		}
		return r.Decide("F2", nil, run.Discuss, "Do we need history?", time.Now())
	})
	_, res = a.do("", "wait", "--timeout", "10ms")
	found := res["findings"].([]any)
	if res["event"] != "discuss" || len(found) != 1 || found[0].(map[string]any)["id"] != "F2" {
		t.Fatalf("wait discuss: %v", res)
	}

	_, res = a.do("", "feedback")
	if res["published"] != false || len(res["open"].([]any)) != 1 {
		t.Fatalf("feedback before publishing: %v", res)
	}

	_, res = a.do("", "reply", "F2", "--text", "Finance audits need it.", "--proposal", "Add deleted_at and keep rows", "--severity", "minor")
	f := res["finding"].(map[string]any)
	if f["status"] != "pending" || f["rev"] != float64(2) || f["severity"] != "minor" {
		t.Fatalf("reply: %v", res)
	}
	if _, res = a.do("", "reply", "F2", "--text", "again"); errCode(res) != "not-in-discussion" {
		t.Fatalf("second reply: %v", res)
	}
	if code, res := a.do("", "reply", "--text", "x"); code != 1 || errCode(res) != "invalid-args" {
		t.Fatalf("reply without an id: %v", res)
	}

	a.decide(id, func(r *run.Run) error {
		if err := r.Decide("F2", nil, run.Amended, "Only for invoices", time.Now()); err != nil {
			return err
		}
		return r.Publish("Ship it", time.Now())
	})
	_, res = a.do("", "wait", "--timeout", "10ms")
	if res["event"] != "published" || res["note"] != "Ship it" {
		t.Fatalf("wait published: %v", res)
	}
	apply := res["apply"].([]any)[0].(map[string]any)
	if apply["id"] != "F1" || apply["proposal"] != "Use a flag" || apply["file"] != filepath.Join(realDir(t, dir), "plan.md") {
		t.Fatalf("apply entry: %v", apply)
	}
	tweak := res["applyWithChanges"].([]any)[0].(map[string]any)
	if tweak["id"] != "F2" || tweak["suggestion"] != "Only for invoices" || tweak["thread"] == nil {
		t.Fatalf("applyWithChanges entry: %v", tweak)
	}
	if _, res = a.do("", "feedback"); res["published"] != true || res["note"] != "Ship it" {
		t.Fatalf("feedback after publishing: %v", res)
	}
	if _, res = a.do("", "summary", "--text", "late"); errCode(res) != "published" {
		t.Fatalf("summary after publishing: %v", res)
	}
}

func realDir(t *testing.T, dir string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestHandoffRefusals(t *testing.T) {
	a, _ := newAgent(t)
	_, res := a.do("", "start", "plan.md")
	id := res["run"].(string)

	if _, res = a.do("", "handoff"); errCode(res) != "no-findings" {
		t.Fatalf("handoff with no findings: %v", res)
	}
	a.do(`{"title":"t","body":"b"}`, "add")

	// No terminal host in the test environment: the fix tells the human what to run.
	_, res = a.do("", "handoff")
	fix := res["error"].(map[string]any)["fix"].(string)
	if errCode(res) != "no-pane-host" || !strings.Contains(fix, "review --run "+id) || !strings.Contains(fix, "--home") {
		t.Fatalf("handoff without a host: %v", res)
	}

	release, err := run.MarkSession(a.home, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, res = a.do("", "handoff"); errCode(res) != "review-open" {
		t.Fatalf("handoff while the review is open: %v", res)
	}
	release()

	a.decide(id, func(r *run.Run) error {
		if err := r.Decide("F1", nil, run.Rejected, "", time.Now()); err != nil {
			return err
		}
		return r.Publish("", time.Now())
	})
	if _, res = a.do("", "handoff"); errCode(res) != "published" {
		t.Fatalf("handoff after publishing: %v", res)
	}
}

func TestReviewNeedsATerminal(t *testing.T) {
	a, _ := newAgent(t)
	_, res := a.do("", "start", "plan.md")
	id := res["run"].(string)
	// The test's stdin is not a terminal, as an agent's shell is not.
	if code, res := a.do("", "review", "--run", id, "--home", a.home); code != 1 || errCode(res) != "tty" {
		t.Fatalf("review without a terminal: exit %d %v", code, res)
	}
}

func TestAddRejectsMalformedInput(t *testing.T) {
	a, _ := newAgent(t)
	a.do("", "start", "plan.md")
	for name, in := range map[string]string{
		"unknown field": `{"title":"t","body":"b","priority":1}`,
		"trailing data": `{"title":"t","body":"b"} {}`,
		"not JSON":      `title: t`,
		"empty array":   `[]`,
	} {
		if _, res := a.do(in, "add"); errCode(res) != "invalid-input" {
			t.Errorf("%s: %v", name, res)
		}
	}
	if _, res := a.do("", "add", "--from", "missing.json"); errCode(res) != "invalid-input" {
		t.Errorf("missing --from file: %v", res)
	}
}

func TestCommandsBeforeAnyRun(t *testing.T) {
	a, _ := newAgent(t)
	for _, cmd := range []string{"add", "summary", "wait", "feedback", "show", "handoff"} {
		if _, res := a.do("{}", cmd); errCode(res) != "no-run" {
			t.Errorf("%s with no run: %v", cmd, res)
		}
	}
	if _, res := a.do("", "show", "--run", "../etc"); errCode(res) != "unknown-run" {
		t.Errorf("show with a bad id: %v", res)
	}
	if _, list := a.raw("", "list"); !strings.Contains(list, "No runs yet") {
		t.Errorf("list with no runs: %q", list)
	}
}

func TestHelpAndUnknownCommands(t *testing.T) {
	a, _ := newAgent(t)
	for _, args := range [][]string{nil, {"help"}, {"--help"}} {
		if code, out := a.raw("", args...); code != 0 || !strings.Contains(out, "Usage: sift <command>") {
			t.Errorf("sift %v: exit %d\n%s", args, code, out)
		}
	}
	if code, out := a.raw("", "help", "wait"); code != 0 || !strings.Contains(out, "Usage: sift wait") {
		t.Errorf("help wait: exit %d\n%s", code, out)
	}
	if code, out := a.raw("", "add", "-h"); code != 0 || out != "" {
		t.Errorf("add -h: exit %d, stdout %q", code, out)
	}
	if code, _ := a.raw("", "frobnicate"); code != 2 {
		t.Errorf("unknown command: exit %d, want 2", code)
	}
	if code, res := a.do("", "start", "plan.md", "--no-such-flag"); code != 1 || errCode(res) != "error" {
		t.Errorf("unknown flag: exit %d %v", code, res)
	}
}
