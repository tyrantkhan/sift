package tui

import (
	"fmt"
	"html"
	"image/color"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"charm.land/glamour/v2"
	gansi "charm.land/glamour/v2/ansi"
	gstyles "charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	cstyles "github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/x/ansi"
)

// clean drops control and invisible formatting characters, so a reviewed file or finding cannot drive the human's
// terminal with escape sequences or reorder text with bidi controls.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if (unicode.IsControl(r) && r != '\n' && r != '\t') || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
}

// fit cuts a styled line to a width. The renderer would cut it anyway, but silently and mid-word.
func fit(line string, w int) string {
	return ansi.Truncate(strings.ReplaceAll(line, "\n", " "), max(w, 0), "…")
}

// pad fits a styled line to exactly w columns.
func pad(line string, w int) string {
	line = fit(line, w)
	return line + strings.Repeat(" ", max(0, w-lipgloss.Width(line)))
}

// truncate cuts plain text to w columns, counting wide characters as two.
func truncate(s string, w int) string {
	return ansi.Truncate(strings.ReplaceAll(s, "\t", "    "), max(w, 0), "…")
}

// entity matches HTML character references as glamour's decoder reads them, which accepts a missing semicolon.
var entity = regexp.MustCompile(`&(#[0-9]+|#[xX][0-9a-fA-F]+|[A-Za-z][A-Za-z0-9]*);?`)

// defuseEntities escapes HTML character references that decode to control or formatting characters, so they
// show as the literal text the agent wrote. glamour decodes them after clean has run, so &#27; would otherwise
// reach the terminal as a real escape and could hide part of a proposal from the human approving it.
func defuseEntities(s string) string {
	return entity.ReplaceAllStringFunc(s, func(ref string) string {
		if decoded := html.UnescapeString(ref); decoded != ref && clean(decoded) != decoded {
			return "&amp;" + ref[1:]
		}
		return ref
	})
}

// markdown renders finding text, which agents write as Markdown, and caches it by width and text, since a card is
// rebuilt each time the run changes.
type markdown struct {
	dark      bool
	renderers map[int]*glamour.TermRenderer
	out       map[string][]string
	code      map[string][]string
}

func newMarkdown(dark bool) *markdown {
	return &markdown{dark: dark, renderers: map[int]*glamour.TermRenderer{}, out: map[string][]string{},
		code: map[string][]string{}}
}

func (md *markdown) style() gansi.StyleConfig {
	st := gstyles.DarkStyleConfig
	if !md.dark {
		st = gstyles.LightStyleConfig
	}
	// The card supplies the margins and the text color; glamour's own would double them.
	zero := uint(0)
	st.Document.Margin = &zero
	st.Document.BlockPrefix, st.Document.BlockSuffix = "", ""
	st.Document.Color = nil
	return st
}

// lines renders text to styled lines at most width wide.
func (md *markdown) lines(text string, width int) []string {
	// Tabs expand to spaces first: glamour counts a tab as no columns, and the viewport later counts it as four.
	text = defuseEntities(strings.ReplaceAll(clean(strings.TrimSpace(text)), "\t", "    "))
	key := fmt.Sprintf("%d\x00%s", width, text)
	if out, ok := md.out[key]; ok {
		return out
	}
	r, ok := md.renderers[width]
	if !ok {
		var err error
		if r, err = glamour.NewTermRenderer(glamour.WithStyles(md.style()), glamour.WithWordWrap(width)); err != nil {
			r = nil
		}
		md.renderers[width] = r
	}
	var out []string
	if r != nil {
		if s, err := r.Render(text); err == nil {
			out = strings.Split(s, "\n")
		}
	}
	// Plain wrapped text still says everything when Markdown cannot be rendered.
	if out == nil {
		out = strings.Split(lipgloss.NewStyle().Width(width).Render(text), "\n")
	}
	for len(out) > 0 && strings.TrimSpace(ansi.Strip(out[0])) == "" {
		out = out[1:]
	}
	for len(out) > 0 && strings.TrimSpace(ansi.Strip(out[len(out)-1])) == "" {
		out = out[:len(out)-1]
	}
	for i, l := range out {
		// &#9; decodes to a tab inside glamour, after the expansion above.
		out[i] = fit(strings.ReplaceAll(l, "\t", "    "), width)
	}
	md.out[key] = out
	return out
}

// highlight colors source lines by the file's language, keeping one output line per input line, each cut to
// width first so a minified file's megabyte-long lines are never lexed. Results are cached, since a card is
// rebuilt each time the run changes.
func (md *markdown) highlight(path string, start int, lines []string, width int) []string {
	key := fmt.Sprintf("%s\x00%d\x00%d\x00%d", path, start, len(lines), width)
	if out, ok := md.code[key]; ok {
		return out
	}
	src := make([]string, len(lines))
	for i, l := range lines {
		src[i] = truncate(clean(l), width)
	}
	out := highlightLines(path, src, md.dark)
	md.code[key] = out
	return out
}

func highlightLines(path string, src []string, dark bool) []string {
	lexer := lexers.Match(filepath.Base(path))
	if lexer == nil {
		return src
	}
	style := cstyles.Get("catppuccin-latte")
	if dark {
		style = cstyles.Get("catppuccin-mocha")
	}
	it, err := chroma.Coalesce(lexer).Tokenise(nil, strings.Join(src, "\n"))
	if err != nil {
		return src
	}
	out := make([]string, 0, len(src))
	for _, toks := range chroma.SplitTokensIntoLines(it.Tokens()) {
		var b strings.Builder
		for _, tok := range toks {
			e := style.Get(tok.Type)
			st := lipgloss.NewStyle()
			if e.Colour.IsSet() {
				st = st.Foreground(lipgloss.Color(e.Colour.String()))
			}
			if e.Bold == chroma.Yes {
				st = st.Bold(true)
			}
			if e.Italic == chroma.Yes {
				st = st.Italic(true)
			}
			b.WriteString(st.Render(strings.TrimRight(tok.Value, "\n")))
		}
		out = append(out, b.String())
	}
	if len(out) > len(src) {
		out = out[:len(src)]
	}
	for len(out) < len(src) {
		out = append(out, src[len(out)])
	}
	return out
}

// border draws one horizontal edge of a frame, inner columns wide, with labels set into it at the left and right.
func border(left, right, corner1, corner2 string, inner int, c color.Color) string {
	line := lipgloss.NewStyle().Foreground(c)
	if left != "" {
		left = " " + left + " "
	}
	if right != "" {
		right = " " + right + " "
	}
	// One rule cell before each label, so a label never touches the corner.
	room := inner - 1
	if lipgloss.Width(left)+lipgloss.Width(right)+1 > room {
		right = ""
	}
	if lipgloss.Width(left)+1 > room {
		left = fit(left, max(0, room-1))
	}
	fill := max(0, room-lipgloss.Width(left)-lipgloss.Width(right)-1)
	s := line.Render(corner1+"─") + left + line.Render(strings.Repeat("─", fill))
	if right != "" {
		s += right + line.Render("─")
	} else {
		s += line.Render("─")
	}
	return s + line.Render(corner2)
}

// frame draws a rounded box around lines, inner columns wide, with labels in its top and bottom edges.
func frame(lines []string, inner int, c color.Color, topLeft, topRight, bottomLeft, bottomRight string) []string {
	side := lipgloss.NewStyle().Foreground(c).Render("│")
	out := []string{border(topLeft, topRight, "╭", "╮", inner+2, c)}
	for _, l := range lines {
		out = append(out, side+" "+pad(l, inner)+" "+side)
	}
	return append(out, border(bottomLeft, bottomRight, "╰", "╯", inner+2, c))
}

// bar prefixes lines with a colored rule, for proposals, notes and messages.
func bar(lines []string, c color.Color) []string {
	rule := lipgloss.NewStyle().Foreground(c).Render("┃")
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = rule + " " + l
	}
	return out
}
