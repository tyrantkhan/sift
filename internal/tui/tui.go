// Package tui is the human's review interface: one finding on screen at a time, one key to decide it.
package tui

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/tyrantkhan/sift/internal/run"
)

type mode int

const (
	modeIntro mode = iota
	modeCard
	modeList
	modeInput
	modeFinish
	modeHelp
	modeDone
)

type inputKind int

const (
	inputSuggest inputKind = iota
	inputDiscuss
	inputReject
)

type (
	tickMsg  time.Time
	guardMsg struct{}
)

const (
	inputHeight  = 5
	maxCardWidth = 140
	excerptLines = 40
	// stackWidth holds the edges of the cards fanned behind the current one.
	stackWidth = 2
	// The finish screen ignores enter for openGuard after it opens or changes, so an enter meant for the screen
	// before cannot send; sending cannot be undone. openGuard outlasts a slow keyboard's delay before a held key
	// starts repeating, and each enter inside the guard restarts it for sendGuard, longer than a repeat interval.
	openGuard = time.Second
	sendGuard = 600 * time.Millisecond
)

type Model struct {
	root, id string
	run      *run.Run
	docs     run.Docs
	order    []string
	cur      int
	listCur  int
	jump     string
	mode     mode
	back     mode
	kind     inputKind
	ta       textarea.Model
	note     textarea.Model
	vp       viewport.Model
	w, h     int
	flash    string
	gutter   int

	th         theme
	md         *markdown
	now        func() time.Time
	guardUntil time.Time
}

// Options adjusts the interface to the terminal it runs in.
type Options struct {
	// CornerGutter leaves this many columns empty at the right end of the header, for hosts such as Orca
	// that draw pane controls over the top-right corner of a split pane, where the progress would otherwise sit.
	CornerGutter int
}

// Run opens the interface full screen and returns, when the human quits, whether this session published the run.
func Run(root, id string, opts Options) (bool, error) {
	m, err := newModel(root, id, opts)
	if err != nil {
		return false, err
	}
	wasPublished := m.run.Published != nil
	final, err := tea.NewProgram(m).Run()
	if err != nil {
		return false, fmt.Errorf("review interface: %w", err)
	}
	return !wasPublished && final.(Model).run.Published != nil, nil
}

func newModel(root, id string, opts Options) (Model, error) {
	r, docs, err := run.Load(root, id)
	if err != nil {
		return Model{}, err
	}
	m := Model{root: root, id: id, run: r, docs: docs, ta: newTextarea(3), note: newTextarea(3), vp: viewport.New(),
		now: time.Now, gutter: max(0, opts.CornerGutter)}
	// Dark until the terminal reports its background, which is the more common case.
	m.setTheme(true)
	m.reorder()
	m.cur = m.nextPending(-1)
	if r.Published != nil {
		m.mode = modeDone
	}
	return m, nil
}

func newTextarea(height int) textarea.Model {
	ta := textarea.New()
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	ta.CharLimit = 8000
	ta.SetHeight(height)
	// Enter submits, because one key per decision is the point; a newline needs a modifier.
	ta.KeyMap.InsertNewline.SetKeys("alt+enter", "ctrl+j")
	return ta
}

func tick() tea.Cmd {
	return tea.Tick(700*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func guardTick(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return guardMsg{} })
}

// decisionKeys act on a finding at once. Holding one down must not decide card after card as each decision moves
// to the next, so their repeats are dropped wherever the terminal reports them.
var decisionKeys = map[string]bool{"a": true, "x": true, "X": true, "u": true, "s": true, "d": true, "enter": true}

func (m Model) dropRepeat(k tea.KeyPressMsg) bool {
	if !k.IsRepeat {
		return false
	}
	if k.String() == "enter" {
		return true
	}
	return (m.mode == modeCard || m.mode == modeIntro) && decisionKeys[k.String()]
}

func (m Model) Init() tea.Cmd { return tea.Batch(tick(), tea.RequestBackgroundColor) }

func (m *Model) setTheme(dark bool) {
	m.th = newTheme(dark)
	m.md = newMarkdown(dark)
	for _, ta := range []*textarea.Model{&m.ta, &m.note} {
		st := textarea.DefaultStyles(dark)
		st.Focused.CursorLine = lipgloss.NewStyle()
		st.Focused.Placeholder = m.th.fg(m.th.muted)
		ta.SetStyles(st)
	}
}

// agentName and agentTitle name the agent mid-sentence and at the start of one. A name the agent gave is shown
// as given, so only the default is capitalized.
func (m Model) agentName() string {
	if n := clean(m.run.AgentName()); n != "" {
		return n
	}
	return "the agent"
}

func (m Model) agentTitle() string {
	if n := m.agentName(); n != "the agent" {
		return n
	}
	return "The agent"
}

func (m Model) idAt(i int) string {
	if i < 0 || i >= len(m.order) {
		return ""
	}
	return m.order[i]
}

func (m Model) indexOf(id string) int {
	for i, v := range m.order {
		if v == id {
			return i
		}
	}
	return -1
}

// reorder rebuilds the display order and keeps both cursors on the findings they pointed at.
func (m *Model) reorder() {
	keepCur, keepList := m.idAt(m.cur), m.idAt(m.listCur)
	fs := run.Ordered(m.run.Findings)
	m.order = make([]string, len(fs))
	for i, f := range fs {
		m.order[i] = f.ID
	}
	if i := m.indexOf(keepCur); i >= 0 {
		m.cur = i
	}
	if i := m.indexOf(keepList); i >= 0 {
		m.listCur = i
	}
	m.cur = clamp(m.cur, len(m.order))
	m.listCur = clamp(m.listCur, len(m.order))
}

func clamp(i, n int) int { return max(0, min(i, n-1)) }

func (m Model) current() *run.Finding {
	id := m.idAt(m.cur)
	if id == "" {
		return nil
	}
	f, _ := m.run.Finding(id)
	return f
}

// nextPending finds the next finding still waiting on the human after index from, wrapping around.
func (m Model) nextPending(from int) int {
	n := len(m.order)
	for i := 1; i <= n; i++ {
		j := ((from+i)%n + n) % n
		if f, ok := m.run.Finding(m.order[j]); ok && f.Status == run.Pending {
			return j
		}
	}
	return clamp(from, n)
}

// setRun swaps in a newer run and tells the human what the agent did since the last one they saw.
func (m *Model) setRun(r *run.Run) {
	var answered []string
	for _, f := range r.Findings {
		old, ok := m.run.Finding(f.ID)
		if ok && agentMessages(f) > agentMessages(*old) {
			answered = append(answered, f.ID)
		}
	}
	added := len(r.Findings) - len(m.run.Findings)
	m.run = r
	m.reorder()
	m.refresh(false)
	switch {
	case len(answered) > 0:
		m.jump = answered[0]
		m.flash = m.agentTitle() + " answered " + strings.Join(answered, ", ") + "."
		if m.mode == modeCard {
			m.flash += " Press l to jump there."
		}
	case added > 0:
		m.flash = fmt.Sprintf("%s filed %d more finding(s).", m.agentTitle(), added)
	}
}

func agentMessages(f run.Finding) int {
	n := 0
	for _, msg := range f.Thread {
		if msg.Author == run.AuthorAgent {
			n++
		}
	}
	return n
}

func (m *Model) refresh(top bool) {
	m.vp.SetContent(m.cardContent())
	if top {
		m.vp.GotoTop()
	}
}

// Layout, top to bottom: the header bar, a spacer when there is room, the body, the footer.
func (m Model) headerHeight() int {
	if m.h >= 18 {
		return 2
	}
	return 1
}

// footerHeight is the status line and one or two rows of keys, under a rule when there is room.
func (m Model) footerHeight() int {
	h := 3
	if m.oneKeyRow() {
		h--
	}
	if m.h >= 18 {
		h++
	}
	return h
}

func (m Model) bodyHeight() int { return max(1, m.h-m.headerHeight()-m.footerHeight()) }

// cardOuter is the card's full width, borders included, leaving a margin and room for the stack behind it.
func (m Model) cardOuter() int { return max(24, min(m.w-2-stackWidth, maxCardWidth)) }

// cardInner is the width of text inside the card.
func (m Model) cardInner() int { return m.cardOuter() - 4 }

func (m *Model) layout() {
	h := m.bodyHeight()
	if m.mode == modeInput {
		h -= inputHeight
	}
	m.vp.SetWidth(m.cardInner())
	m.vp.SetHeight(max(1, h-2))
	m.ta.SetWidth(m.cardInner())
	// The note sits in a box inside the finish screen's margin, two columns narrower than a card.
	m.note.SetWidth(m.cardInner() - 2)
	m.refresh(false)
}

// reload re-reads run.json every tick; the file snapshots never change. Comparing contents cannot miss a write
// the way comparing modification times can.
func (m *Model) reload() {
	r, err := run.LoadRun(m.root, m.id)
	if err != nil {
		m.flash = "Could not reload the run: " + err.Error()
		return
	}
	if !reflect.DeepEqual(r, m.run) {
		m.setRun(r)
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.layout()
		return m, nil
	case tea.BackgroundColorMsg:
		m.setTheme(msg.IsDark())
		m.refresh(false)
		return m, nil
	case guardMsg:
		// Nothing to change; the render that follows lights up the send button.
		return m, nil
	case tickMsg:
		if m.mode == modeDone {
			return m, tick()
		}
		wasReady := m.run.Ready()
		m.reload()
		// The agent's answer can make the finish screen ready, or not, while the human watches it. The guard
		// restarts, so an enter meant for the screen before cannot send.
		if m.mode == modeFinish && m.run.Ready() != wasReady {
			return m, tea.Batch(tick(), m.focusFinish())
		}
		return m, tick()
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.dropRepeat(msg) {
			return m, nil
		}
		var cmd tea.Cmd
		switch m.mode {
		case modeIntro:
			cmd = m.updateIntro(msg)
		case modeCard:
			cmd = m.updateCard(msg)
		case modeList:
			cmd = m.updateList(msg)
		case modeInput:
			cmd = m.updateInput(msg)
		case modeFinish:
			cmd = m.updateFinish(msg)
		case modeHelp:
			m.mode = m.back
		case modeDone:
			cmd = tea.Quit
		}
		return m, cmd
	}
	// Pastes, and the text boxes' own messages such as a ctrl+v paste or the cursor blink, go to whichever box is
	// open. They never reach the decision keys, so pasted text cannot approve or reject anything.
	var cmd tea.Cmd
	switch {
	case m.mode == modeInput:
		m.ta, cmd = m.ta.Update(msg)
	case m.mode == modeFinish && m.run.Ready():
		m.note, cmd = m.note.Update(msg)
	}
	return m, cmd
}

func (m *Model) updateIntro(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "enter", "space", "right":
		m.mode = modeCard
		m.refresh(true)
	case "l":
		m.openList()
	case "?":
		m.back, m.mode = modeIntro, modeHelp
	case "q":
		return tea.Quit
	}
	return nil
}

func (m *Model) openList() {
	m.listCur = m.cur
	if i := m.indexOf(m.jump); i >= 0 {
		m.listCur = i
	}
	m.jump = ""
	m.mode = modeList
}

func (m *Model) updateCard(k tea.KeyPressMsg) tea.Cmd {
	f := m.current()
	if f == nil {
		switch k.String() {
		case "q":
			return tea.Quit
		case "?":
			m.back, m.mode = modeCard, modeHelp
		}
		return nil
	}
	m.flash = ""
	switch k.String() {
	case "a":
		m.commit(run.Approved, "")
	case "x":
		m.commit(run.Rejected, "")
	case "u":
		m.commit(run.Pending, "")
	case "s":
		prefill := ""
		if f.Status == run.Amended {
			prefill = f.Note
		}
		return m.openInput(inputSuggest, prefill)
	case "X":
		prefill := ""
		if f.Status == run.Rejected {
			prefill = f.Note
		}
		return m.openInput(inputReject, prefill)
	case "d":
		return m.openInput(inputDiscuss, "")
	case "right", "n", "tab":
		m.cur = (m.cur + 1) % len(m.order)
		m.refresh(true)
	case "left", "b", "shift+tab":
		m.cur = (m.cur - 1 + len(m.order)) % len(m.order)
		m.refresh(true)
	case "l":
		m.openList()
	case "f", "P":
		return m.openFinish()
	case "?":
		m.back, m.mode = modeCard, modeHelp
	case "q":
		return tea.Quit
	default:
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(k)
		return cmd
	}
	return m.focusFinish()
}

// commit saves a decision on the current card, guarded by the state the human saw, then moves on to the next
// finding waiting on them. It reports whether the decision was saved.
func (m *Model) commit(s run.Status, note string) bool {
	f := m.current()
	if f == nil {
		return false
	}
	id, seen := f.ID, run.SeenOf(*f)
	r, err := run.Update(m.root, m.id, func(r *run.Run, _ run.Docs) error {
		return r.Decide(id, &seen, s, note, time.Now())
	})
	if err != nil {
		m.reload()
		m.flash = "Not saved: " + err.Error()
		return false
	}
	m.setRun(r)
	if s == run.Pending {
		return true
	}
	// With nothing left for the human, the finish screen says whether to publish or what the agent still owes.
	if r.Counts().Pending == 0 {
		m.openFinish()
		return true
	}
	m.cur = m.nextPending(m.cur)
	m.refresh(true)
	return true
}

func (m *Model) openInput(kind inputKind, prefill string) tea.Cmd {
	if f := m.current(); f != nil && f.Status == run.Withdrawn {
		m.flash = m.agentTitle() + " withdrew this one. Press u to reinstate it first."
		return nil
	}
	m.kind = kind
	m.mode = modeInput
	m.ta.Reset()
	m.ta.SetValue(prefill)
	m.layout()
	return m.ta.Focus()
}

func (m *Model) closeInput() {
	m.ta.Blur()
	m.mode = modeCard
	m.layout()
}

func (m *Model) updateInput(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "esc":
		m.closeInput()
		return nil
	case "enter":
		text := strings.TrimSpace(m.ta.Value())
		if text == "" && m.kind != inputReject {
			m.flash = "Type something first, or esc to cancel."
			return nil
		}
		status := map[inputKind]run.Status{inputSuggest: run.Amended, inputDiscuss: run.Discuss, inputReject: run.Rejected}[m.kind]
		// The input stays open on a failed save, so the human's note survives to retry or copy.
		if !m.commit(status, text) {
			return nil
		}
		if m.mode == modeInput {
			m.closeInput()
		}
		return m.focusFinish()
	}
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(k)
	return cmd
}

// openFinish shows the finish screen: the publish form when everything is decided, else what is left.
func (m *Model) openFinish() tea.Cmd {
	if len(m.run.Findings) == 0 {
		m.flash = "No findings yet. " + m.agentTitle() + " adds them with sift add."
		return nil
	}
	if m.mode != modeFinish {
		m.back = modeCard
		if m.mode == modeList {
			m.back = modeList
		}
	}
	m.mode = modeFinish
	m.ta.Blur()
	m.layout()
	return m.focusFinish()
}

// focusFinish gives the note focus only while the run can be sent, so keys never reach a hidden box, and starts
// the send guard. Moving to an unfinished finding needs no guard; it cannot be undone only when it sends.
func (m *Model) focusFinish() tea.Cmd {
	if m.mode != modeFinish || !m.run.Ready() {
		m.note.Blur()
		m.guardUntil = time.Time{}
		return nil
	}
	m.guardUntil = m.now().Add(openGuard)
	return tea.Batch(m.note.Focus(), guardTick(openGuard))
}

func (m Model) guarded() bool { return m.now().Before(m.guardUntil) }

func (m *Model) leaveFinish(to mode) {
	m.note.Blur()
	m.mode = to
	m.layout()
}

// unfinished returns the ids still waiting on the human and those waiting on the agent, in display order.
func (m Model) unfinished() (pending, discuss []string) {
	for _, id := range m.order {
		f, _ := m.run.Finding(id)
		switch f.Status {
		case run.Pending:
			pending = append(pending, id)
		case run.Discuss:
			discuss = append(discuss, id)
		}
	}
	return pending, discuss
}

func (m *Model) updateFinish(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "esc":
		m.leaveFinish(m.back)
		return nil
	case "enter":
		if !m.run.Ready() {
			pending, discuss := m.unfinished()
			target := slices.Concat(pending, discuss)
			if len(target) == 0 {
				return nil
			}
			m.cur = m.indexOf(target[0])
			m.flash = ""
			m.leaveFinish(modeCard)
			m.refresh(true)
			return nil
		}
		// Each enter inside the guard restarts it, so a held-down enter, whose repeats arrive as presses, never
		// reaches the end of the guard.
		if m.guarded() {
			if t := m.now().Add(sendGuard); t.After(m.guardUntil) {
				m.guardUntil = t
			}
			return guardTick(m.guardUntil.Sub(m.now()))
		}
		note := m.note.Value()
		r, err := run.Update(m.root, m.id, func(r *run.Run, _ run.Docs) error {
			return r.Publish(note, time.Now())
		})
		if err != nil {
			m.reload()
			m.flash = "Not sent: " + err.Error()
			return nil
		}
		m.setRun(r)
		m.leaveFinish(modeDone)
		// Exiting closes the pane in Herdr, tmux, Orca and iTerm2, so the human lands back with the agent.
		return tea.Quit
	}
	if !m.run.Ready() {
		return nil
	}
	var cmd tea.Cmd
	m.note, cmd = m.note.Update(k)
	return cmd
}

func (m *Model) updateList(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "up", "k":
		m.listCur = clamp(m.listCur-1, len(m.order))
	case "down", "j":
		m.listCur = clamp(m.listCur+1, len(m.order))
	case "enter", "space":
		if len(m.order) == 0 {
			return nil
		}
		m.cur = m.listCur
		m.mode = modeCard
		m.refresh(true)
	case "esc", "l":
		m.mode = modeCard
	case "f", "P":
		return m.openFinish()
	case "q":
		return tea.Quit
	}
	return nil
}
