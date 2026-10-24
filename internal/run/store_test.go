package run

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func env(vars map[string]string) func(string) string { return func(k string) string { return vars[k] } }

func TestDataRoot(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		vars map[string]string
		want string
	}{
		{map[string]string{"SIFT_HOME": "/srv/sift", "XDG_DATA_HOME": "/xdg"}, "/srv/sift"},
		{map[string]string{"XDG_DATA_HOME": "/xdg"}, "/xdg/sift"},
		{nil, filepath.Join(home, ".local", "share", "sift")},
	}
	for _, c := range cases {
		got, err := DataRoot(env(c.vars))
		if err != nil || got != c.want {
			t.Errorf("DataRoot(%v) = %q, %v; want %q", c.vars, got, err, c.want)
		}
	}
}

func TestResolvePicksTheRun(t *testing.T) {
	root := t.TempDir()
	if _, err := Resolve(root, "", env(nil)); code(err) != "no-run" {
		t.Fatalf("no runs: err = %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("# a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := Create(root, Start{Paths: []string{"a.md"}, Cwd: dir, Title: "first"}, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Create(root, Start{Paths: []string{"a.md"}, Cwd: dir, Title: "second"}, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		flag string
		vars map[string]string
		want string
	}{
		"latest":       {"", nil, second.ID},
		"SIFT_RUN":     {"", map[string]string{"SIFT_RUN": first.ID}, first.ID},
		"flag wins":    {first.ID, map[string]string{"SIFT_RUN": second.ID}, first.ID},
		"flag is used": {second.ID, nil, second.ID},
	} {
		if got, err := Resolve(root, c.flag, env(c.vars)); err != nil || got != c.want {
			t.Errorf("%s: Resolve = %q, %v; want %q", name, got, err, c.want)
		}
	}
	if _, err := Resolve(root, "nope-20260101-000000", env(nil)); code(err) != "unknown-run" {
		t.Errorf("missing run: err = %v", err)
	}
	if _, err := Resolve(root, "Bad ID", env(nil)); code(err) != "unknown-run" {
		t.Errorf("invalid id: err = %v", err)
	}
}

func TestListSkipsUnreadableRuns(t *testing.T) {
	root := t.TempDir()
	if runs, skipped, err := List(root); err != nil || runs != nil || skipped != nil {
		t.Fatalf("empty root: %v %v %v", runs, skipped, err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("# a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	older, _ := Create(root, Start{Paths: []string{"a.md"}, Cwd: dir, Title: "older"}, now)
	newer, _ := Create(root, Start{Paths: []string{"a.md"}, Cwd: dir, Title: "newer"}, now.Add(time.Minute))
	// An interrupted start, and a run from another schema version.
	if err := os.MkdirAll(filepath.Join(runsDir(root), "broken-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]any{"version": 1, "id": "old-1"})
	if err := os.MkdirAll(Dir(root, "old-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(Dir(root, "old-1"), runFile), b, 0o644); err != nil {
		t.Fatal(err)
	}
	runs, skipped, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[0].ID != newer.ID || runs[1].ID != older.ID {
		t.Fatalf("runs = %v, want newest first", runs)
	}
	if len(skipped) != 2 || !strings.Contains(skipped[0].Reason+skipped[1].Reason, "schema version 1") {
		t.Fatalf("skipped = %+v", skipped)
	}
}

func TestReviewSessionLock(t *testing.T) {
	root, r := newRun(t)
	if open, err := OpenSession(root, r.ID); err != nil || open {
		t.Fatalf("before review: open=%v err=%v", open, err)
	}
	release, err := MarkSession(root, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if open, err := OpenSession(root, r.ID); err != nil || !open {
		t.Fatalf("during review: open=%v err=%v", open, err)
	}
	if _, err := MarkSession(root, r.ID); code(err) != "review-open" {
		t.Fatalf("second review: err = %v", err)
	}
	release()
	if open, _ := OpenSession(root, r.ID); open {
		t.Fatal("the lock outlived the review")
	}
}

func TestStorageErrNamesTheSandboxFix(t *testing.T) {
	for _, err := range []error{fs.ErrPermission, syscall.EROFS} {
		if got := storageErr("writing x", err); code(got) != "data-root" {
			t.Errorf("%v: %v, want a data-root refusal", err, got)
		}
	}
	other := storageErr("writing x", errors.New("disk on fire"))
	if code(other) != "" || !strings.Contains(other.Error(), "disk on fire") {
		t.Errorf("other errors pass through wrapped: %v", other)
	}
	if err := writeAtomic(filepath.Join(t.TempDir(), "missing", "f"), []byte("x")); err == nil {
		t.Error("writing into a missing directory should fail")
	}
}

func TestLoadRefusesOtherSchemaVersions(t *testing.T) {
	root, r := newRun(t)
	p := filepath.Join(Dir(root, r.ID), runFile)
	b, _ := os.ReadFile(p)
	if err := os.WriteFile(p, []byte(strings.Replace(string(b), `"version": 2`, `"version": 3`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(root, r.ID); err == nil || !strings.Contains(err.Error(), "schema version 3") {
		t.Fatalf("err = %v", err)
	}
	if err := os.WriteFile(p, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRun(root, r.ID); err == nil {
		t.Fatal("corrupt run.json should not load")
	}
}

func TestReplyValidation(t *testing.T) {
	_, r := newRun(t)
	ids, err := r.Add(Docs{"plan.md": ParseDoc([]byte(samplePlan), "plan.md")}, "", []NewFinding{{Title: "a", Body: "b"}})
	if err != nil {
		t.Fatal(err)
	}
	id := ids[0]
	if err := r.Decide(id, nil, Discuss, "why?", now); err != nil {
		t.Fatal(err)
	}
	str := func(s string) *string { return &s }
	for name, c := range map[string]struct {
		text string
		rev  Revision
	}{
		"no text":      {"", Revision{}},
		"blank title":  {"x", Revision{Title: str(" ")}},
		"escape title": {"x", Revision{Title: str("a\x1b[8m")}},
		"empty body":   {"x", Revision{Body: str("  ")}},
		"bad severity": {"x", Revision{Severity: str("urgent")}},
	} {
		if err := r.Reply(id, c.text, c.rev, false, now); code(err) != "invalid-input" {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if err := r.Reply("F99", "x", Revision{}, false, now); code(err) != "unknown-finding" {
		t.Errorf("unknown finding: err = %v", err)
	}
	if err := r.Reply(id, "fair, dropping it", Revision{Body: str("new body")}, true, now); err != nil {
		t.Fatal(err)
	}
	f, _ := r.Finding(id)
	if f.Status != Withdrawn || f.Body != "new body" || f.Rev != 2 {
		t.Fatalf("after withdrawing: %+v", f)
	}
	if err := r.Publish("", now); err != nil {
		t.Fatalf("a withdrawn finding counts as decided: %v", err)
	}
	if err := r.SetSummary("late"); code(err) != "published" {
		t.Errorf("summary after publishing: err = %v", err)
	}
}

func TestAddRefusesBadFindings(t *testing.T) {
	_, r := newRun(t)
	docs := Docs{"plan.md": ParseDoc([]byte(samplePlan), "plan.md")}
	for name, nf := range map[string]NewFinding{
		"no body":        {Title: "t"},
		"long title":     {Title: strings.Repeat("x", 121), Body: "b"},
		"two-word label": {Title: "t", Body: "b", Label: "two words"},
		"empty anchor":   {Title: "t", Body: "b", Anchor: &AnchorInput{}},
	} {
		if _, err := r.Add(docs, "", []NewFinding{nf}); code(err) != "invalid-input" {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := r.Add(docs, "", nil); code(err) != "invalid-input" {
		t.Errorf("no findings: err = %v", err)
	}
}

func TestOrderingAndCounts(t *testing.T) {
	fs := []Finding{{ID: "F1", Severity: "minor"}, {ID: "F2"}, {ID: "F3", Severity: "critical"}, {ID: "F4", Severity: "minor"}}
	var got []string
	for _, f := range Ordered(fs) {
		got = append(got, f.ID)
	}
	if strings.Join(got, ",") != "F3,F1,F4,F2" {
		t.Errorf("Ordered = %v, want most severe first and filing order within a severity", got)
	}
	r := &Run{Findings: []Finding{{Status: Approved}, {Status: Amended}, {Status: Rejected}, {Status: Withdrawn}, {Status: Discuss}, {Status: Pending}}}
	if c := r.Counts(); c.Decided() != 4 || c.Discuss != 1 || c.Pending != 1 {
		t.Errorf("Counts = %+v", c)
	}
	if r.AgentName() != "the agent" {
		t.Errorf("default agent name = %q", r.AgentName())
	}
	var rf error = &Refusal{Message: "m"}
	if rf.Error() != "m" {
		t.Errorf("Refusal.Error = %q", rf.Error())
	}
}

func TestExcerptClampsToTheFile(t *testing.T) {
	d := ParseDoc([]byte("a\nb\nc\n"), "x.txt")
	if lines, omitted := d.Excerpt(Anchor{StartLine: 2, EndLine: 9}, 10); len(lines) != 2 || omitted != 0 {
		t.Errorf("past the end: %q %d", lines, omitted)
	}
	if lines, _ := d.Excerpt(Anchor{StartLine: 7, EndLine: 9}, 10); lines != nil {
		t.Errorf("outside the file: %q", lines)
	}
	if lines, omitted := d.Excerpt(Anchor{StartLine: 1, EndLine: 3}, 2); len(lines) != 2 || omitted != 1 {
		t.Errorf("capped: %q %d", lines, omitted)
	}
}
