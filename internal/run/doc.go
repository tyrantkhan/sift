package run

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
)

// Doc is the snapshot of one reviewed file taken at start, so anchors stay valid while the agent edits the original.
type Doc struct {
	Lines []string
	// Markdown files alone have headings to anchor to; in code, '#' starts a comment or a directive.
	Markdown bool
}

// Docs holds each reviewed file's snapshot by its path in the run.
type Docs map[string]Doc

func ParseDoc(b []byte, path string) Doc {
	d := Doc{Markdown: IsMarkdown(path)}
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s != "" {
		d.Lines = strings.Split(s, "\n")
	}
	return d
}

func IsMarkdown(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown", ".mdx":
		return true
	}
	return false
}

// Reviewable reports whether a file's content can be shown as text lines: not empty and not binary.
func Reviewable(b []byte) bool {
	return len(bytes.TrimSpace(b)) > 0 && bytes.IndexByte(b[:min(len(b), 8000)], 0) < 0
}

type Heading struct {
	Level int    `json:"level"`
	Line  int    `json:"line"`
	Text  string `json:"text"`
}

// Headings lists a Markdown file's ATX headings, skipping fenced code blocks where '#' starts comments.
func (p Doc) Headings() []Heading {
	if !p.Markdown {
		return nil
	}
	var hs []Heading
	fence := ""
	for i, l := range p.Lines {
		indent := len(l) - len(strings.TrimLeft(l, " "))
		t := strings.TrimSpace(l)
		if indent > 3 {
			continue
		}
		if marker := fenceMarker(t); marker != "" {
			if fence == "" {
				fence = marker
				continue
			}
			// Only a bare fence of the same character, at least as long, closes the block.
			if marker[0] == fence[0] && len(marker) >= len(fence) && marker == t {
				fence = ""
				continue
			}
		}
		if fence != "" || !strings.HasPrefix(t, "#") {
			continue
		}
		level := len(t) - len(strings.TrimLeft(t, "#"))
		rest := t[level:]
		if level > 6 || (rest != "" && rest[0] != ' ') {
			continue
		}
		hs = append(hs, Heading{Level: level, Line: i + 1, Text: headingText(rest)})
	}
	return hs
}

func fenceMarker(t string) string {
	for _, c := range []string{"`", "~"} {
		if n := len(t) - len(strings.TrimLeft(t, c)); n >= 3 {
			return t[:n]
		}
	}
	return ""
}

// headingText drops an optional closing sequence of #s, which counts only after a space, so "C#" keeps its #.
func headingText(rest string) string {
	rest = strings.TrimSpace(rest)
	trimmed := strings.TrimRight(rest, "#")
	if trimmed == "" {
		return ""
	}
	if strings.HasSuffix(trimmed, " ") {
		return strings.TrimSpace(trimmed)
	}
	return rest
}

func normalizeHeading(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// Resolve turns an anchor input into explicit lines. Lines win when given; a heading alone covers its whole section.
func (p Doc) Resolve(in AnchorInput) (*Anchor, error) {
	n := len(p.Lines)
	if in.StartLine == 0 && in.EndLine == 0 {
		if strings.TrimSpace(in.Heading) == "" {
			return nil, refuse("invalid-input", "give anchor.heading, anchor.startLine, or omit anchor for a general finding",
				"anchor is empty")
		}
		return p.resolveHeading(in.Heading)
	}
	start, end := in.StartLine, in.EndLine
	if end == 0 {
		end = start
	}
	if start < 1 || end < start || end > n {
		return nil, refuse("invalid-anchor", fmt.Sprintf("use lines between 1 and %d with startLine <= endLine", n),
			"lines %d-%d are outside the file", start, end)
	}
	return &Anchor{Heading: strings.TrimSpace(in.Heading), StartLine: start, EndLine: end}, nil
}

func describeHeadings(hs []Heading, idx []int) string {
	names := make([]string, 0, len(idx))
	for _, i := range idx {
		names = append(names, fmt.Sprintf("%q (line %d)", hs[i].Text, hs[i].Line))
	}
	if len(names) > 40 {
		names = append(names[:40], "...")
	}
	return strings.Join(names, ", ")
}

// resolveHeading matches exactly (case-insensitive), else as the one heading containing the text. More than one
// candidate is refused rather than guessed, since a wrong section shows the human the wrong excerpt.
func (p Doc) resolveHeading(want string) (*Anchor, error) {
	hs := p.Headings()
	w := normalizeHeading(strings.TrimLeft(strings.TrimSpace(want), "#"))
	var exact, partial []int
	for i, h := range hs {
		switch n := normalizeHeading(h.Text); {
		case n == w:
			exact = append(exact, i)
		case w != "" && strings.Contains(n, w):
			partial = append(partial, i)
		}
	}
	cands := exact
	if len(cands) == 0 {
		cands = partial
	}
	switch {
	case len(cands) == 0:
		all := make([]int, len(hs))
		for i := range hs {
			all[i] = i
		}
		return nil, refuse("invalid-anchor", "use one of: "+describeHeadings(hs, all), "no heading matches %q", want)
	case len(cands) > 1:
		return nil, refuse("invalid-anchor", "anchor by startLine and endLine instead; candidates: "+describeHeadings(hs, cands),
			"heading %q matches %d sections", want, len(cands))
	}
	match := cands[0]
	h := hs[match]
	end := len(p.Lines)
	for _, next := range hs[match+1:] {
		if next.Level <= h.Level {
			end = next.Line - 1
			break
		}
	}
	for end > h.Line && strings.TrimSpace(p.Lines[end-1]) == "" {
		end--
	}
	return &Anchor{Heading: h.Text, StartLine: h.Line, EndLine: end}, nil
}

// Excerpt returns at most max lines of the anchor's range and how many lines it left out.
func (p Doc) Excerpt(a Anchor, max int) (lines []string, omitted int) {
	start, end := a.StartLine, a.EndLine
	if start < 1 || start > len(p.Lines) {
		return nil, 0
	}
	if end > len(p.Lines) {
		end = len(p.Lines)
	}
	total := end - start + 1
	if total > max {
		end = start + max - 1
		omitted = total - max
	}
	return p.Lines[start-1 : end], omitted
}
