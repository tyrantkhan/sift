package tui

import (
	"fmt"
	"image/color"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/tyrantkhan/sift/internal/run"
)

func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	// Where the terminal supports it, held keys arrive marked as repeats, which dropRepeat ignores.
	v.KeyboardEnhancements.ReportEventTypes = true
	v.WindowTitle = "sift · " + clean(m.run.Title)
	return v
}

func (m Model) render() string {
	if m.w == 0 {
		return "loading…"
	}
	if m.w < 50 || m.h < 14 {
		return fmt.Sprintf("Make the window at least 50×14 (now %d×%d).", m.w, m.h)
	}
	var body []string
	switch m.mode {
	case modeIntro:
		body = m.introView()
	case modeCard:
		body = m.cardView(m.bodyHeight())
	case modeInput:
		body = append(m.cardView(m.bodyHeight()-inputHeight), m.inputView()...)
	case modeList:
		body = m.listView()
	case modeFinish:
		body = m.finishView()
	case modeHelp:
		body = m.helpView()
	case modeDone:
		body = m.doneView()
	}
	lines := []string{m.header()}
	if m.headerHeight() > 1 {
		lines = append(lines, "")
	}
	h := m.bodyHeight()
	for i := range h {
		l := ""
		if i < len(body) {
			l = body[i]
		}
		lines = append(lines, fit(l, m.w))
	}
	return strings.Join(append(lines, m.footer()...), "\n")
}

// indentLines puts the body's left margin on lines that do not draw their own.
func indentLines(lines []string, n int) []string {
	sp := strings.Repeat(" ", n)
	out := make([]string, len(lines))
	for i, l := range lines {
		if l != "" {
			l = sp + l
		}
		out[i] = l
	}
	return out
}

// header is the bar in the logo's indigo: the name, the run's title, and one cell per finding colored by its
// decision, so the pattern of the review shows at a glance.
func (m Model) header() string {
	t := m.th
	onBar := func(c color.Color) lipgloss.Style {
		st := lipgloss.NewStyle().Background(t.indigo)
		if c != nil {
			st = st.Foreground(c)
		}
		return st
	}
	tab := lipgloss.NewStyle().Background(t.violet).Foreground(t.white).Bold(true).Render(" sift ")
	c := m.run.Counts()

	cells := ""
	room := m.w / 3
	if len(m.order) <= room-6 {
		for i, id := range m.order {
			f, _ := m.run.Finding(id)
			cell := "▮"
			col := t.good
			switch f.Status {
			case run.Pending:
				cell, col = "▯", t.lilac
			case run.Amended:
				col = t.tweak
			case run.Rejected:
				col = t.bad
			case run.Discuss:
				col = t.talk
			case run.Withdrawn:
				col = t.muted
			}
			st := onBar(col)
			if (m.mode == modeCard || m.mode == modeInput) && i == m.cur {
				st = st.Underline(true)
			}
			cells += st.Render(cell)
		}
	} else {
		// Too many findings for a cell each: a proportional bar of decided against total.
		n := max(1, room-6)
		done := 0
		if c.Total > 0 {
			done = c.Decided() * n / c.Total
		}
		cells = onBar(t.good).Render(strings.Repeat("▮", done)) + onBar(t.lilac).Render(strings.Repeat("▯", n-done))
	}
	right := cells + onBar(t.white).Bold(true).Render(fmt.Sprintf(" %d/%d ", c.Decided(), c.Total))
	titleRoom := m.w - lipgloss.Width(tab) - lipgloss.Width(right) - 2
	title := onBar(t.lilac).Render(" " + truncate(clean(m.run.Title), max(1, titleRoom)))
	gap := max(0, m.w-lipgloss.Width(tab)-lipgloss.Width(title)-lipgloss.Width(right))
	return fit(tab+title+onBar(nil).Render(strings.Repeat(" ", gap))+right, m.w)
}

// counts is the status line: what is left first, so a narrow pane cuts the least useful parts.
func (m Model) counts() string {
	t := m.th
	c := m.run.Counts()
	var parts []string
	add := func(n int, glyph, label string, col color.Color) {
		if n > 0 {
			parts = append(parts, t.fg(col).Render(fmt.Sprintf("%s %d", glyph, n))+" "+t.fg(t.muted).Render(label))
		}
	}
	add(c.Pending, "○", "to go", t.accent)
	add(c.Approved, "✓", "approved", t.good)
	add(c.Amended, "✎", "tweaked", t.tweak)
	add(c.Rejected, "✗", "rejected", t.bad)
	add(c.Discuss, "…", "with "+m.agentName(), t.talk)
	add(c.Withdrawn, "–", "withdrawn", t.muted)
	return strings.Join(parts, "   ")
}

func (m Model) keys(pairs ...string) string {
	out := make([]string, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, m.th.key(pairs[i], pairs[i+1]))
	}
	return " " + strings.Join(out, "  ")
}

// footer is a status line and two rows of keys: what to decide on top, where to go below. Two short rows fit a
// split pane where one long row would be cut off.
func (m Model) footer() []string {
	var top, bottom string
	switch m.mode {
	case modeIntro:
		top = m.keys("enter", "start", "l", "list")
		bottom = m.keys("?", "help", "q", "quit")
	case modeCard:
		top = m.keys("a", "approve", "s", "suggest", "x", "reject", "d", "discuss", "u", "undo")
		bottom = m.keys("←→", "move", "l", "list", "f", "finish", "?", "help", "q", "quit")
	case modeList:
		top = m.keys("↑↓", "move", "enter", "open", "esc", "back")
		bottom = m.keys("f", "finish", "q", "quit")
	case modeInput:
		top = m.keys("enter", "save", "esc", "cancel")
		bottom = m.keys("alt+enter", "new line")
	case modeFinish:
		if m.run.Ready() {
			top = m.keys("enter", "send to "+m.agentName(), "esc", "back")
			bottom = m.keys("alt+enter", "new line")
		} else {
			top = m.keys("enter", "go there", "esc", "back")
		}
	default:
		top = m.keys("any key", "continue")
	}
	status := " " + m.counts()
	if m.flash != "" {
		status = " " + m.th.fg(m.th.warn).Render("! "+clean(m.flash))
	}
	return []string{fit(status, m.w), fit(top, m.w), fit(bottom, m.w)}
}

// cardContent is the scrolling inside of the current finding's card.
func (m Model) cardContent() string {
	f := m.current()
	if f == nil || m.w == 0 {
		return ""
	}
	t := m.th
	w := m.cardInner()
	var out []string
	blank := func() { out = append(out, "") }

	title := lipgloss.NewStyle().Bold(true).Width(w).Render(clean(f.Title))
	out = append(out, strings.Split(title, "\n")...)
	blank()
	out = append(out, m.md.lines(f.Body, w)...)

	if strings.TrimSpace(f.Proposal) != "" {
		blank()
		heading := t.fg(t.good).Bold(true).Width(w).Render("If you approve, " + m.agentName() + " will")
		out = append(out, strings.Split(heading, "\n")...)
		out = append(out, bar(m.md.lines(f.Proposal, w-2), t.good)...)
	}
	switch {
	case f.Status == run.Amended:
		blank()
		out = append(out, t.fg(t.tweak).Bold(true).Render("Your tweak"))
		out = append(out, bar(m.md.lines(f.Note, w-2), t.tweak)...)
	case f.Status == run.Rejected && f.Note != "":
		blank()
		out = append(out, t.fg(t.bad).Bold(true).Render("Why you rejected it"))
		out = append(out, bar(m.md.lines(f.Note, w-2), t.bad)...)
	}

	if f.Anchor != nil {
		blank()
		out = append(out, m.excerpt(*f.Anchor, w)...)
	}

	if len(f.Thread) > 0 {
		blank()
		out = append(out, t.fg(t.muted).Render("Conversation"))
		for _, msg := range f.Thread {
			who, col, indent := "You", t.talk, 0
			if msg.Author == run.AuthorAgent {
				who, col, indent = m.agentTitle(), t.accent, 2
			}
			lines := append([]string{t.fg(col).Bold(true).Render(who)}, m.md.lines(msg.Text, w-2-indent)...)
			out = append(out, indentLines(bar(lines, col), indent)...)
		}
	}
	return strings.Join(out, "\n")
}

// excerpt frames the lines a finding points at, with the file and range in the frame's edge.
func (m Model) excerpt(a run.Anchor, w int) []string {
	t := m.th
	where := fmt.Sprintf("lines %d–%d", a.StartLine, a.EndLine)
	if a.StartLine == a.EndLine {
		where = fmt.Sprintf("line %d", a.StartLine)
	}
	if a.Heading != "" {
		where = clean(a.Heading) + "  " + where
	}
	lines, omitted := m.docs[a.Path].Excerpt(a, excerptLines)
	numW := len(fmt.Sprint(a.StartLine + len(lines)))
	inner := w - 4
	var body []string
	for i, l := range m.md.highlight(a.Path, a.StartLine, lines, inner-numW-1) {
		num := t.fg(t.muted).Render(fmt.Sprintf("%*d ", numW, a.StartLine+i))
		body = append(body, num+fit(l, inner-numW-1))
	}
	bottom := ""
	if omitted > 0 {
		bottom = t.fg(t.muted).Render(fmt.Sprintf("%d more lines", omitted))
	}
	top := t.fg(t.accent).Render(clean(a.Path)) + " " + t.fg(t.muted).Render(where)
	return frame(body, inner, t.faint, top, "", bottom, "")
}

// cardView is the current finding as a card, with the edges of the findings still to decide fanned behind it.
// The stack thins as the human works through it.
func (m Model) cardView(height int) []string {
	f := m.current()
	if f == nil {
		return indentLines([]string{"", m.th.fg(m.th.muted).Render("No findings in this run yet. " + m.agentTitle() +
			" adds them with sift add.")}, 2)
	}
	t := m.th
	inner := m.cardInner()
	vpLines := strings.Split(m.vp.View(), "\n")
	content := make([]string, max(1, height-2))
	for i := range content {
		if i < len(vpLines) {
			content[i] = vpLines[i]
		}
	}
	sev := f.Severity
	if sev == "" {
		sev = "unrated"
	}
	edge := t.severity(f.Severity)
	topLeft := t.pill(sev, edge)
	if f.Label != "" {
		topLeft += " " + t.fg(t.accent).Render(clean(f.Label))
	}
	if f.Rev > 1 {
		topLeft += " " + t.fg(t.muted).Render(fmt.Sprintf("revised %d×", f.Rev-1))
	}
	bottomLeft := t.fg(t.accent).Bold(true).Render(f.ID) + " " + t.fg(t.muted).Render(fmt.Sprintf("card %d of %d", m.cur+1, len(m.order)))
	scroll := ""
	switch {
	case !m.vp.AtBottom():
		scroll = t.fg(t.accent).Render("↓ more")
	case !m.vp.AtTop():
		scroll = t.fg(t.muted).Render("↑ top")
	}
	card := frame(content, inner, edge, topLeft, t.statusText(f.Status, m.agentName()), bottomLeft, scroll)

	behind := 0
	for _, id := range m.order {
		if g, _ := m.run.Finding(id); g.Status == run.Pending && g.ID != f.ID {
			behind++
		}
	}
	layers := min(behind, stackWidth)
	edgeStyle := t.fg(t.faint)
	for row := range card {
		var s strings.Builder
		for k := 1; k <= stackWidth; k++ {
			switch {
			case k > layers || row < k:
				s.WriteString(" ")
			case row == k:
				s.WriteString(edgeStyle.Render("╮"))
			case row == len(card)-1:
				s.WriteString(edgeStyle.Render("╯"))
			default:
				s.WriteString(edgeStyle.Render("│"))
			}
		}
		card[row] = " " + card[row] + s.String()
	}
	return card
}

func (m Model) inputView() []string {
	t := m.th
	prompt := map[inputKind]string{
		inputSuggest: "Your tweak, for " + m.agentName() + " to apply",
		inputDiscuss: "Your question for " + m.agentName(),
		inputReject:  "Why reject it? (optional)",
	}[m.kind]
	edge := map[inputKind]color.Color{inputSuggest: t.tweak, inputDiscuss: t.talk, inputReject: t.bad}[m.kind]
	box := frame(strings.Split(m.ta.View(), "\n"), m.cardInner(), edge, t.fg(edge).Bold(true).Render(prompt), "", "", "")
	return indentLines(box, 1)
}

func (m Model) introView() []string {
	t := m.th
	w := m.cardInner()
	c := m.run.Counts()
	var out []string
	if m.bodyHeight() > 12 {
		out = append(out, "")
	}
	out = append(out, lipgloss.NewStyle().Bold(true).Render(fit(clean(m.run.Title), w)))
	var files []string
	for _, f := range m.run.Files {
		files = append(files, clean(f.Path))
	}
	list := strings.Join(files, "  ")
	if lipgloss.Width(list) > w && len(files) > 1 {
		list = fmt.Sprintf("%s and %d more files", files[0], len(files)-1)
	}
	out = append(out, t.fg(t.muted).Render(fit(list, w)), "")
	if m.run.Summary != "" {
		out = append(out, bar(m.md.lines(m.run.Summary, w-2), t.lilac)...)
		out = append(out, "")
	}
	out = append(out, fmt.Sprintf("%d findings to decide, most severe first.", c.Pending))
	out = append(out, t.fg(t.muted).Render("One key each. Every decision saves at once."))
	return indentLines(out, 2)
}

func (m Model) listView() []string {
	t := m.th
	height := m.bodyHeight()
	start := 0
	if m.listCur >= height-1 {
		start = m.listCur - height + 2
	}
	out := []string{""}
	for i := start; i < len(m.order) && i < start+height-1; i++ {
		f, _ := m.run.Finding(m.order[i])
		g, gc := t.status(f.Status)
		cursor := "  "
		title := clean(f.Title)
		// In a multi-file round, the file says which part of the work a finding is about.
		file := ""
		if len(m.run.Files) > 1 && f.Anchor != nil {
			file = "  " + filepath.Base(clean(f.Anchor.Path))
		}
		room := max(10, m.w-14-ansi.StringWidth(file))
		title = truncate(title, room)
		if i == m.listCur {
			cursor = t.fg(t.accent).Bold(true).Render("▌ ")
			title = lipgloss.NewStyle().Bold(true).Render(title)
		}
		dot := t.fg(t.severity(f.Severity)).Render("●")
		out = append(out, " "+cursor+t.fg(gc).Render(g)+" "+dot+" "+t.fg(t.muted).Render(fmt.Sprintf("%-4s", f.ID))+
			title+t.fg(t.muted).Render(file))
	}
	return out
}

// button is a filled call to action; while the send guard runs it shows dimmed, so an ignored enter is explained.
func (m Model) button(label string, c color.Color) string {
	t := m.th
	st := lipgloss.NewStyle().Foreground(t.white).Bold(true).Padding(0, 2).Background(c)
	if m.guarded() {
		st = st.Background(t.muted)
	}
	return st.Render(label + "  ⏎")
}

// finishView fits the body rows of the smallest supported window, 50x14, so the note box is never cut off.
func (m Model) finishView() []string {
	t := m.th
	c := m.run.Counts()
	w := m.cardInner()
	var out []string
	if m.bodyHeight() > 10 {
		out = append(out, "")
	}
	if !m.run.Ready() {
		pending, discuss := m.unfinished()
		out = append(out, lipgloss.NewStyle().Bold(true).Render("Not finished yet"), "")
		if len(pending) > 0 {
			out = append(out, t.fg(t.accent).Render(fit("○ to decide: "+strings.Join(pending, ", "), w)))
		}
		if len(discuss) > 0 {
			out = append(out, t.fg(t.talk).Render(fit("… waiting on "+m.agentName()+": "+strings.Join(discuss, ", "), w)))
		}
		out = append(out, "")
		if len(pending) > 0 {
			out = append(out, m.button("Go to "+pending[0], t.violet))
		} else if len(discuss) > 0 {
			out = append(out, strings.Split(t.fg(t.muted).Width(w).Render(m.agentTitle()+"'s answer shows up here."), "\n")...)
			out = append(out, m.button("Decide "+discuss[0]+" yourself", t.violet))
		}
		return indentLines(out, 2)
	}
	var parts []string
	add := func(n int, glyph, text string, col color.Color) {
		if n > 0 {
			parts = append(parts, t.fg(col).Render(fmt.Sprintf("%s %d", glyph, n))+" "+t.fg(t.muted).Render(text))
		}
	}
	add(c.Approved, "✓", "apply", t.good)
	add(c.Amended, "✎", "with your tweak", t.tweak)
	add(c.Rejected, "✗", "skip", t.bad)
	add(c.Withdrawn, "–", "withdrawn", t.muted)
	out = append(out, lipgloss.NewStyle().Bold(true).Render(fit("All decided. Send it to "+m.agentName()+"?", w)))
	out = append(out, fit(strings.Join(parts, "   "), w))
	box := frame(strings.Split(m.note.View(), "\n"), w-2, t.accent,
		t.fg(t.accent).Render("Anything else for "+m.agentName()+"?")+t.fg(t.muted).Render(" optional"), "", "", "")
	out = append(out, box...)
	out = append(out, m.button("Send to "+m.agentName(), t.good))
	return indentLines(out, 1)
}

func (m Model) doneView() []string {
	t := m.th
	out := []string{"", t.fg(t.good).Bold(true).Render("✓ Sent to " + m.agentName() + ".")}
	if m.run.Published != nil {
		out = append(out, t.fg(t.muted).Render("Sent "+m.run.Published.At.Local().Format("Mon 15:04")+". Any key closes this."))
	}
	return indentLines(out, 2)
}

func (m Model) helpView() []string {
	t := m.th
	a := m.agentName()
	rows := [][2]string{
		{"a", a + " makes its proposed change"},
		{"s", "approve with your tweak"},
		{"x X", "reject, or reject and say why"},
		{"d", "ask " + a + "; decide after it answers"},
		{"u", "undo, or reinstate a withdrawn one"},
		{"← →", "previous and next finding"},
		{"↑ ↓", "scroll the card"},
		{"l", "list every finding"},
		{"f", "send, or see what is left"},
		{"q", "quit; decisions are already saved"},
	}
	var body []string
	for _, r := range rows {
		k := t.fg(t.accent).Bold(true).Render(fmt.Sprintf(" %-3s", r[0]))
		body = append(body, k+" "+r[1])
	}
	// A short window shows the keys without the frame rather than cut it.
	if len(body)+2 > m.bodyHeight() {
		return indentLines(body, 2)
	}
	return indentLines(frame(body, m.cardInner(), t.faint, t.fg(t.accent).Render("Keys"), "", "", ""), 1)
}
