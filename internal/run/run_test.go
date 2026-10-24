package run

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const samplePlan = `# Plan

## Data model
Users table.
Orders table.

### Indexes
idx on user_id

## Rollout
` + "```sh\n# not a heading\n```" + `
Ship it.
`

var now = time.Date(2026, 10, 24, 14, 0, 0, 0, time.UTC)

func newRun(t *testing.T) (string, *Run) {
	t.Helper()
	root := t.TempDir()
	p := filepath.Join(t.TempDir(), "My Doc.md")
	if err := os.WriteFile(p, []byte(samplePlan), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := Create(root, Start{Paths: []string{p}, Cwd: filepath.Dir(p)}, now)
	if err != nil {
		t.Fatal(err)
	}
	return root, r
}

func code(err error) string {
	var rf *Refusal
	if errors.As(err, &rf) {
		return rf.Code
	}
	return ""
}

func TestHeadingsSkipFencedCode(t *testing.T) {
	hs := ParseDoc([]byte(samplePlan), "plan.md").Headings()
	var texts []string
	for _, h := range hs {
		texts = append(texts, h.Text)
	}
	want := []string{"Plan", "Data model", "Indexes", "Rollout"}
	if len(texts) != len(want) {
		t.Fatalf("headings = %v, want %v", texts, want)
	}
	for i := range want {
		if texts[i] != want[i] {
			t.Fatalf("headings = %v, want %v", texts, want)
		}
	}
}

func TestResolveHeadingCoversSectionIncludingSubsections(t *testing.T) {
	p := ParseDoc([]byte(samplePlan), "plan.md")
	a, err := p.Resolve(AnchorInput{Heading: "data model"})
	if err != nil {
		t.Fatal(err)
	}
	if a.StartLine != 3 || a.EndLine != 8 {
		t.Fatalf("anchor = %+v, want lines 3-8", a)
	}
	if _, err := p.Resolve(AnchorInput{Heading: "nope"}); code(err) != "invalid-anchor" {
		t.Fatalf("unknown heading: err = %v", err)
	}
	if _, err := p.Resolve(AnchorInput{StartLine: 5, EndLine: 99}); code(err) != "invalid-anchor" {
		t.Fatalf("out of range: err = %v", err)
	}
}

func TestAddIsAllOrNothing(t *testing.T) {
	root, r := newRun(t)
	_, err := Update(root, r.ID, func(r *Run, p Docs) error {
		_, err := r.Add(p, "", []NewFinding{
			{Title: "ok", Body: "b"},
			{Title: "bad", Body: "b", Severity: "huge"},
		})
		return err
	})
	if code(err) != "invalid-input" {
		t.Fatalf("err = %v, want invalid-input", err)
	}
	got, _, _ := Load(root, r.ID)
	if len(got.Findings) != 0 {
		t.Fatalf("stored %d findings from a refused batch", len(got.Findings))
	}
}

func TestDecisionLoop(t *testing.T) {
	root, r := newRun(t)
	r, err := Update(root, r.ID, func(r *Run, p Docs) error {
		_, err := r.Add(p, "", []NewFinding{
			{Title: "a", Body: "b", Proposal: "do x"},
			{Title: "c", Body: "d"},
			{Title: "e", Body: "f", Anchor: &AnchorInput{Heading: "Rollout"}},
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Decide("F1", nil, Approved, "", now); err != nil {
		t.Fatal(err)
	}
	if err := r.Decide("F2", nil, Amended, "", now); code(err) != "invalid-input" {
		t.Fatalf("empty suggestion: err = %v", err)
	}
	if err := r.Decide("F2", nil, Amended, "only for admins", now); err != nil {
		t.Fatal(err)
	}
	if err := r.Reply("F3", "hi", Revision{}, false, now); code(err) != "not-in-discussion" {
		t.Fatalf("reply before discuss: err = %v", err)
	}
	if err := r.Decide("F3", nil, Discuss, "why?", now); err != nil {
		t.Fatal(err)
	}
	if err := r.Publish("", now); code(err) != "not-ready" {
		t.Fatalf("publish with open discussion: err = %v", err)
	}
	title := "sharper"
	if err := r.Reply("F3", "because", Revision{Title: &title}, false, now); err != nil {
		t.Fatal(err)
	}
	f, _ := r.Finding("F3")
	if f.Status != Pending || f.Rev != 2 || f.Title != "sharper" || len(f.Thread) != 2 {
		t.Fatalf("after reply: %+v", f)
	}
	if err := r.Decide("F3", nil, Rejected, "not now", now); err != nil {
		t.Fatal(err)
	}
	if err := r.Publish("thanks", now); err != nil {
		t.Fatal(err)
	}
	if err := r.Decide("F1", nil, Rejected, "", now); code(err) != "published" {
		t.Fatalf("decide after publish: err = %v", err)
	}
}

func TestConcurrentUpdatesKeepEveryWrite(t *testing.T) {
	root, r := newRun(t)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := Update(root, r.ID, func(r *Run, p Docs) error {
				_, err := r.Add(p, "", []NewFinding{{Title: "t", Body: "b"}})
				return err
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	got, _, _ := Load(root, r.ID)
	if len(got.Findings) != 20 {
		t.Fatalf("findings = %d, want 20", len(got.Findings))
	}
	seen := map[string]bool{}
	for _, f := range got.Findings {
		if seen[f.ID] {
			t.Fatalf("duplicate id %s", f.ID)
		}
		seen[f.ID] = true
	}
}

func TestResolveRejectsPathTraversal(t *testing.T) {
	root, _ := newRun(t)
	if _, err := Resolve(root, "../../etc", func(string) string { return "" }); code(err) != "unknown-run" {
		t.Fatalf("err = %v", err)
	}
}

func TestHeadingEdgeCases(t *testing.T) {
	p := ParseDoc([]byte("# Plan\n## Phase 1\n### Testing\na\n## Phase 2\n### Testing\nb\n## Support for C#\n"+
		"````md\n```\n# inside\n```\n````\n## After ##\n    # indented code\n"), "plan.md")
	var texts []string
	for _, h := range p.Headings() {
		texts = append(texts, h.Text)
	}
	want := "Plan|Phase 1|Testing|Phase 2|Testing|Support for C#|After"
	if got := strings.Join(texts, "|"); got != want {
		t.Fatalf("headings = %s, want %s", got, want)
	}
	if _, err := p.Resolve(AnchorInput{Heading: "Testing"}); code(err) != "invalid-anchor" {
		t.Fatalf("duplicate heading: err = %v", err)
	}
	if _, err := p.Resolve(AnchorInput{Heading: "phase"}); code(err) != "invalid-anchor" {
		t.Fatalf("ambiguous partial: err = %v", err)
	}
	if a, err := p.Resolve(AnchorInput{Heading: "Support for C#"}); err != nil || a.StartLine != 8 {
		t.Fatalf("C# heading: %+v %v", a, err)
	}
}

func TestDecideGuards(t *testing.T) {
	root, r := newRun(t)
	r, err := Update(root, r.ID, func(r *Run, p Docs) error {
		_, err := r.Add(p, "", []NewFinding{{Title: "a", Body: "b"}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	f, _ := r.Finding("F1")
	seen := SeenOf(*f)
	if err := r.Decide("F1", nil, Discuss, "q?", now); err != nil {
		t.Fatal(err)
	}
	if err := r.Decide("F1", &seen, Approved, "", now); code(err) != "stale" {
		t.Fatalf("stale: err = %v", err)
	}
	if err := r.Reply("F1", "dropping it", Revision{}, true, now); err != nil {
		t.Fatal(err)
	}
	if err := r.Decide("F1", nil, Approved, "", now); code(err) != "withdrawn" {
		t.Fatalf("approve withdrawn: err = %v", err)
	}
	if err := r.Decide("F1", nil, Pending, "", now); err != nil {
		t.Fatalf("reinstate: %v", err)
	}
}

func TestMultiFileAnchors(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("docs/spec.md", "# Spec\n## Storage\nuse s3\n")
	write("cmd/main.go", "package main\n\n# not a heading\nfunc main() {}\n")
	root := t.TempDir()
	r, err := Create(root, Start{Paths: []string{"docs/spec.md", "cmd/main.go", "./docs/spec.md"}, Cwd: dir}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Files) != 2 || r.Files[0].Path != "docs/spec.md" || r.Title != "spec.md and 1 more" {
		t.Fatalf("files = %+v title = %q", r.Files, r.Title)
	}
	add := func(a AnchorInput) (*Run, error) {
		return Update(root, r.ID, func(r *Run, d Docs) error {
			_, err := r.Add(d, "", []NewFinding{{Title: "t", Body: "b", Anchor: &a}})
			return err
		})
	}
	if _, err := add(AnchorInput{StartLine: 1}); code(err) != "invalid-anchor" {
		t.Fatalf("no path in a multi-file run: err = %v", err)
	}
	if _, err := add(AnchorInput{Path: "cmd/main.go", Heading: "not a heading"}); code(err) != "invalid-anchor" {
		t.Fatalf("heading in a Go file: err = %v", err)
	}
	if _, err := add(AnchorInput{Path: "nope.go", StartLine: 1}); code(err) != "invalid-anchor" {
		t.Fatalf("unknown path: err = %v", err)
	}
	got, err := add(AnchorInput{Path: filepath.Join(dir, "cmd/main.go"), StartLine: 4})
	if err != nil {
		t.Fatal(err)
	}
	if a := got.Findings[0].Anchor; a.Path != "cmd/main.go" || a.StartLine != 4 {
		t.Fatalf("anchor = %+v, want cmd/main.go line 4 from an absolute path", a)
	}
	got, err = add(AnchorInput{Path: "docs/spec.md", Heading: "storage"})
	if err != nil {
		t.Fatal(err)
	}
	if a := got.Findings[1].Anchor; a.Path != "docs/spec.md" || a.StartLine != 2 || a.EndLine != 3 {
		t.Fatalf("anchor = %+v", a)
	}
	_, docs, err := Load(root, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if lines, _ := docs["cmd/main.go"].Excerpt(*got.Findings[0].Anchor, 5); len(lines) != 1 || lines[0] != "func main() {}" {
		t.Fatalf("excerpt = %q", lines)
	}
}

func TestCreateRefusesBadInput(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bin"), []byte("a\x00b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte("# p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := map[string]Start{
		"binary":        {Paths: []string{"bin"}, Cwd: dir},
		"missing":       {Paths: []string{"nope.md"}, Cwd: dir},
		"none":          {Cwd: dir},
		"control agent": {Paths: []string{"plan.md"}, Cwd: dir, Agent: "\x1b]52;c;aGk=\x07"},
		"bidi agent":    {Paths: []string{"plan.md"}, Cwd: dir, Agent: "Codex\u202e"},
		"wide agent":    {Paths: []string{"plan.md"}, Cwd: dir, Agent: strings.Repeat("界", 13)},
	}
	for name, st := range cases {
		if _, err := Create(t.TempDir(), st, now); code(err) == "" {
			t.Errorf("%s: err = %v, want a refusal", name, err)
		}
	}
}

func TestCreateCountsOneFileOnce(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte("# p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("plan.md", filepath.Join(dir, "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
	r, err := Create(t.TempDir(), Start{Paths: []string{"CLAUDE.md", "plan.md", "./plan.md"}, Cwd: dir}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Files) != 1 || r.Files[0].Path != "CLAUDE.md" || r.Title != "CLAUDE.md" {
		t.Fatalf("files = %+v title = %q, want one file shown as the agent named it", r.Files, r.Title)
	}
}
