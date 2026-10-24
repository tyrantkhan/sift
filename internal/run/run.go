// Package run holds one review round of a set of files: its findings, the human's decisions and the agent's replies.
package run

import (
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const SchemaVersion = 2

type Status string

const (
	Pending   Status = "pending"
	Approved  Status = "approved"
	Amended   Status = "amended"
	Rejected  Status = "rejected"
	Discuss   Status = "discuss"
	Withdrawn Status = "withdrawn"
)

var Severities = []string{"critical", "major", "minor", "trivial"}

// SeverityRank orders severities most severe first; an unstated severity sorts last.
func SeverityRank(s string) int {
	if i := slices.Index(Severities, s); i >= 0 {
		return i
	}
	return len(Severities)
}

type Anchor struct {
	Path      string `json:"path"`
	Heading   string `json:"heading,omitempty"`
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
}

const (
	AuthorHuman = "human"
	AuthorAgent = "agent"
)

type Message struct {
	Author string    `json:"author"`
	Text   string    `json:"text"`
	At     time.Time `json:"at"`
}

type Finding struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Body     string    `json:"body"`
	Proposal string    `json:"proposal,omitempty"`
	Severity string    `json:"severity,omitempty"`
	Label    string    `json:"label,omitempty"`
	Anchor   *Anchor   `json:"anchor,omitempty"`
	Status   Status    `json:"status"`
	Note     string    `json:"note,omitempty"`
	Thread   []Message `json:"thread,omitempty"`
	Rev      int       `json:"rev"`
}

type Publication struct {
	At   time.Time `json:"at"`
	Note string    `json:"note,omitempty"`
}

// File is one reviewed file. Path is how the human and agent name it: relative to the run's root when inside it.
type File struct {
	Path  string `json:"path"`
	Abs   string `json:"abs"`
	Lines int    `json:"lines"`
}

type Run struct {
	Version   int          `json:"version"`
	ID        string       `json:"id"`
	Title     string       `json:"title"`
	Agent     string       `json:"agent,omitempty"`
	Root      string       `json:"root"`
	Files     []File       `json:"files"`
	CreatedAt time.Time    `json:"createdAt"`
	Summary   string       `json:"summary,omitempty"`
	Findings  []Finding    `json:"findings"`
	Published *Publication `json:"published,omitempty"`
}

// Refusal is an expected, user-facing failure. Code is stable for agents to branch on; Fix names what to do next.
type Refusal struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Fix     string `json:"fix,omitempty"`
}

func (r *Refusal) Error() string { return r.Message }

func refuse(code, fix, format string, args ...any) *Refusal {
	return &Refusal{Code: code, Message: fmt.Sprintf(format, args...), Fix: fix}
}

// AgentName is how the review interface refers to the agent: the name it gave at start, else "the agent".
func (r *Run) AgentName() string {
	if r.Agent != "" {
		return r.Agent
	}
	return "the agent"
}

func (r *Run) Finding(id string) (*Finding, bool) {
	for i := range r.Findings {
		if r.Findings[i].ID == id {
			return &r.Findings[i], true
		}
	}
	return nil, false
}

type Counts struct {
	Total     int `json:"total"`
	Pending   int `json:"pending"`
	Approved  int `json:"approved"`
	Amended   int `json:"amended"`
	Rejected  int `json:"rejected"`
	Discuss   int `json:"discuss"`
	Withdrawn int `json:"withdrawn"`
}

func (c Counts) Decided() int { return c.Approved + c.Amended + c.Rejected + c.Withdrawn }

func (r *Run) Counts() Counts {
	c := Counts{Total: len(r.Findings)}
	for _, f := range r.Findings {
		switch f.Status {
		case Pending:
			c.Pending++
		case Approved:
			c.Approved++
		case Amended:
			c.Amended++
		case Rejected:
			c.Rejected++
		case Discuss:
			c.Discuss++
		case Withdrawn:
			c.Withdrawn++
		}
	}
	return c
}

func (r *Run) Ready() bool {
	c := r.Counts()
	return c.Total > 0 && c.Pending == 0 && c.Discuss == 0
}

func (r *Run) refuseIfPublished() error {
	if r.Published != nil {
		return refuse("published", "start a new round with sift start", "run %s was already published", r.ID)
	}
	return nil
}

// NewFinding is the agent's input to Add.
type NewFinding struct {
	Title    string       `json:"title"`
	Body     string       `json:"body"`
	Proposal string       `json:"proposal,omitempty"`
	Severity string       `json:"severity,omitempty"`
	Label    string       `json:"label,omitempty"`
	Anchor   *AnchorInput `json:"anchor,omitempty"`
}

type AnchorInput struct {
	Path      string `json:"path,omitempty"`
	Heading   string `json:"heading,omitempty"`
	StartLine int    `json:"startLine,omitempty"`
	EndLine   int    `json:"endLine,omitempty"`
}

const maxTitle = 120

// hasControl reports control or invisible formatting characters, which would let text drive or disguise itself in
// the human's terminal.
func hasControl(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) >= 0
}

func validateTitle(t string) error {
	if t == "" || len([]rune(t)) > maxTitle || hasControl(t) {
		return refuse("invalid-input", "write a one-line title of at most 120 characters, with detail in body",
			"title must be one line of at most %d plain characters", maxTitle)
	}
	return nil
}

func validateSeverity(s string) error {
	if s != "" && !slices.Contains(Severities, s) {
		return refuse("invalid-input", "use one of "+strings.Join(Severities, ", ")+" or omit it", "unknown severity %q", s)
	}
	return nil
}

func validateLabel(s string) error {
	if len(s) > 40 || strings.ContainsAny(s, " ") || hasControl(s) {
		return refuse("invalid-input", "use one short word such as risk, gap, question or simplification",
			"label %q must be one word of at most 40 characters", s)
	}
	return nil
}

// Add validates every entry before storing any, so a batch is stored entirely or not at all.
// Add files findings. cwd is the agent's working directory, so an anchor path it gives relative to that directory
// resolves too; empty skips that.
func (r *Run) Add(docs Docs, cwd string, in []NewFinding) ([]string, error) {
	if err := r.refuseIfPublished(); err != nil {
		return nil, err
	}
	if len(in) == 0 {
		return nil, refuse("invalid-input", "pass one finding object or a non-empty array", "no findings in the input")
	}
	next := r.nextNumber()
	added := make([]Finding, 0, len(in))
	for i, nf := range in {
		f, err := r.buildFinding(docs, cwd, nf)
		if err != nil {
			if rf, ok := err.(*Refusal); ok {
				rf.Message = fmt.Sprintf("entry %d: %s", i, rf.Message)
			}
			return nil, err
		}
		f.ID = "F" + strconv.Itoa(next+i)
		added = append(added, f)
	}
	r.Findings = append(r.Findings, added...)
	ids := make([]string, len(added))
	for i, f := range added {
		ids[i] = f.ID
	}
	return ids, nil
}

func (r *Run) buildFinding(docs Docs, cwd string, nf NewFinding) (Finding, error) {
	title := strings.TrimSpace(nf.Title)
	if title == "" || strings.TrimSpace(nf.Body) == "" {
		return Finding{}, refuse("invalid-input", "give every finding a title and a body", "title and body are required")
	}
	if err := validateTitle(title); err != nil {
		return Finding{}, err
	}
	if err := validateSeverity(nf.Severity); err != nil {
		return Finding{}, err
	}
	if err := validateLabel(nf.Label); err != nil {
		return Finding{}, err
	}
	f := Finding{Title: title, Body: nf.Body, Proposal: nf.Proposal, Severity: nf.Severity, Label: nf.Label, Status: Pending, Rev: 1}
	if nf.Anchor != nil {
		a, err := r.resolveAnchor(docs, cwd, *nf.Anchor)
		if err != nil {
			return Finding{}, err
		}
		f.Anchor = a
	}
	return f, nil
}

func (r *Run) paths() string {
	ps := make([]string, len(r.Files))
	for i, f := range r.Files {
		ps[i] = f.Path
	}
	if len(ps) > 40 {
		ps = append(ps[:40], "...")
	}
	return strings.Join(ps, ", ")
}

// fileFor finds the reviewed file an anchor names: by its path in the run, or by a path relative to the run's root
// or to the agent's own directory. A path that could mean two files is refused rather than guessed, since a wrong
// file shows the human the wrong lines. A run of one file needs no path.
func (r *Run) fileFor(p, cwd string) (File, error) {
	if p == "" {
		if len(r.Files) == 1 {
			return r.Files[0], nil
		}
		return File{}, refuse("invalid-anchor", "give anchor.path, one of: "+r.paths(),
			"this run reviews %d files, so an anchor needs a path", len(r.Files))
	}
	var hits []File
	hit := func(f File) {
		if !slices.ContainsFunc(hits, func(h File) bool { return h.Abs == f.Abs }) {
			hits = append(hits, f)
		}
	}
	for _, f := range r.Files {
		if f.Path == p {
			hit(f)
		}
	}
	bases := []string{r.Root}
	if cwd != "" {
		bases = append(bases, cwd)
	}
	for _, base := range bases {
		abs := p
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(base, p)
		}
		abs = realPath(filepath.Clean(abs))
		for _, f := range r.Files {
			if f.Abs == abs {
				hit(f)
			}
		}
	}
	switch len(hits) {
	case 0:
		return File{}, refuse("invalid-anchor", "use one of: "+r.paths(), "%q is not a file in this run", p)
	case 1:
		return hits[0], nil
	}
	names := make([]string, len(hits))
	for i, h := range hits {
		names[i] = h.Path
	}
	return File{}, refuse("invalid-anchor", "use the path exactly as sift start printed it: "+strings.Join(names, " or "),
		"%q could mean %s", p, strings.Join(names, " or "))
}

func (r *Run) resolveAnchor(docs Docs, cwd string, in AnchorInput) (*Anchor, error) {
	f, err := r.fileFor(in.Path, cwd)
	if err != nil {
		return nil, err
	}
	doc := docs[f.Path]
	if in.StartLine == 0 && in.EndLine == 0 && strings.TrimSpace(in.Heading) != "" && !doc.Markdown {
		return nil, refuse("invalid-anchor", "anchor by startLine and endLine; headings work only in Markdown files",
			"%s is not Markdown, so it has no headings", f.Path)
	}
	a, err := doc.Resolve(in)
	if err != nil {
		return nil, err
	}
	a.Path = f.Path
	return a, nil
}

func (r *Run) nextNumber() int {
	n := 0
	for _, f := range r.Findings {
		if v, err := strconv.Atoi(strings.TrimPrefix(f.ID, "F")); err == nil && v > n {
			n = v
		}
	}
	return n + 1
}

func (r *Run) mustFinding(id string) (*Finding, error) {
	f, ok := r.Finding(id)
	if !ok {
		return nil, refuse("unknown-finding", "run sift show to list finding ids", "no finding %s in run %s", id, r.ID)
	}
	return f, nil
}

// Seen is a finding's state as the human saw it when deciding. A mismatch means the agent changed it meanwhile.
type Seen struct {
	Status Status
	Rev    int
	Thread int
}

func SeenOf(f Finding) Seen { return Seen{Status: f.Status, Rev: f.Rev, Thread: len(f.Thread)} }

// Decide records the human's decision. Amended and Discuss require a note; Discuss sends the note to the agent.
// A withdrawn finding only goes back to pending, so the human reinstates it before deciding it.
func (r *Run) Decide(id string, seen *Seen, s Status, note string, now time.Time) error {
	if err := r.refuseIfPublished(); err != nil {
		return err
	}
	f, err := r.mustFinding(id)
	if err != nil {
		return err
	}
	if seen != nil && *seen != SeenOf(*f) {
		return refuse("stale", "read the finding again, then decide", "%s changed %s since you opened it", r.AgentName(), id)
	}
	if f.Status == Withdrawn && s != Pending {
		return refuse("withdrawn", "press u to reinstate it, then decide", "%s withdrew %s", r.AgentName(), id)
	}
	note = strings.TrimSpace(note)
	switch s {
	case Approved, Rejected, Pending:
		f.Note = note
	case Amended:
		if note == "" {
			return refuse("invalid-input", "write the change you want applied", "a suggestion needs text")
		}
		f.Note = note
	case Discuss:
		if note == "" {
			return refuse("invalid-input", "write what you want to ask the agent", "a discussion needs text")
		}
		f.Note = ""
		f.Thread = append(f.Thread, Message{Author: AuthorHuman, Text: note, At: now})
	default:
		return fmt.Errorf("status %q is not a human decision", s)
	}
	f.Status = s
	return nil
}

func (r *Run) SetSummary(text string) error {
	if err := r.refuseIfPublished(); err != nil {
		return err
	}
	r.Summary = strings.TrimSpace(text)
	return nil
}

// Ordered sorts findings most severe first, then in filing order, which is the order the human decides them in.
func Ordered(fs []Finding) []Finding {
	out := append([]Finding(nil), fs...)
	sort.SliceStable(out, func(i, j int) bool { return SeverityRank(out[i].Severity) < SeverityRank(out[j].Severity) })
	return out
}

// Revision holds the fields an agent changes when it answers a discussion; nil leaves a field as it is.
type Revision struct {
	Title    *string
	Body     *string
	Proposal *string
	Severity *string
}

// Reply answers the human's discussion note. The finding returns to pending, or to withdrawn when the agent drops it.
func (r *Run) Reply(id, text string, rev Revision, withdraw bool, now time.Time) error {
	if err := r.refuseIfPublished(); err != nil {
		return err
	}
	f, err := r.mustFinding(id)
	if err != nil {
		return err
	}
	if f.Status != Discuss {
		return refuse("not-in-discussion", "run sift wait to learn which findings the human sent back",
			"finding %s is %s, not awaiting a reply", id, f.Status)
	}
	if strings.TrimSpace(text) == "" {
		return refuse("invalid-input", "pass --text with your answer to the human's note", "a reply needs text")
	}
	changed := false
	if rev.Title != nil {
		t := strings.TrimSpace(*rev.Title)
		if err := validateTitle(t); err != nil {
			return err
		}
		f.Title, changed = t, true
	}
	if rev.Body != nil {
		if strings.TrimSpace(*rev.Body) == "" {
			return refuse("invalid-input", "omit --body to keep the current body", "body cannot be empty")
		}
		f.Body, changed = *rev.Body, true
	}
	if rev.Proposal != nil {
		f.Proposal, changed = *rev.Proposal, true
	}
	if rev.Severity != nil {
		if err := validateSeverity(*rev.Severity); err != nil {
			return err
		}
		f.Severity, changed = *rev.Severity, true
	}
	if changed {
		f.Rev++
	}
	f.Thread = append(f.Thread, Message{Author: AuthorAgent, Text: strings.TrimSpace(text), At: now})
	f.Status = Pending
	if withdraw {
		f.Status = Withdrawn
	}
	return nil
}

func (r *Run) Publish(note string, now time.Time) error {
	if err := r.refuseIfPublished(); err != nil {
		return err
	}
	if !r.Ready() {
		c := r.Counts()
		return refuse("not-ready", "decide every pending finding and wait for the agent's replies",
			"%d pending and %d awaiting the agent", c.Pending, c.Discuss)
	}
	r.Published = &Publication{At: now, Note: strings.TrimSpace(note)}
	return nil
}
