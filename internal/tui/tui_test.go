package tui

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/tyrantkhan/sift/internal/run"
)

func setup(t *testing.T) Model {
	t.Helper()
	root := t.TempDir()
	p := filepath.Join(t.TempDir(), "plan.md")
	if err := os.WriteFile(p, []byte("# Plan\n\n## Rollout\nShip Friday.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := run.Create(root, run.Start{Paths: []string{p}, Cwd: filepath.Dir(p), Title: "Plan", Agent: "Codex"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, err = run.Update(root, r.ID, func(r *run.Run, pl run.Docs) error {
		_, err := r.Add(pl, "", []run.NewFinding{
			{Title: "minor one", Body: "b", Severity: "minor"},
			{Title: "critical one", Body: "b", Severity: "critical", Proposal: "add a flag", Anchor: &run.AnchorInput{Heading: "Rollout"}},
			{Title: "major one", Body: "b", Severity: "major"},
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	m, err := newModel(root, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return next.(Model)
}

func key(k string) tea.KeyPressMsg {
	switch k {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	}
	return tea.KeyPressMsg{Code: []rune(k)[0], Text: k}
}

func press(t *testing.T, m Model, keys ...string) Model {
	t.Helper()
	for _, k := range keys {
		next, _ := m.Update(key(k))
		m = next.(Model)
	}
	return m
}

// screen is what the human sees, without the styling.
func screen(m Model) string { return ansi.Strip(m.render()) }

// settle lets the send guard run out, as it does 0.6s after the finish screen opens.
func settle(m Model) Model {
	m.guardUntil = time.Time{}
	return m
}

func status(t *testing.T, m Model, id string) run.Status {
	t.Helper()
	r, _, err := run.Load(m.root, m.id)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := r.Finding(id)
	return f.Status
}

func TestMostSevereFirstAndAutoAdvance(t *testing.T) {
	m := press(t, setup(t), "enter")
	if f := m.current(); f.ID != "F2" {
		t.Fatalf("first card = %s, want the critical F2", f.ID)
	}
	if !strings.Contains(screen(m), "If you approve, Codex will") {
		t.Fatal("card does not show the proposal")
	}
	m = press(t, m, "a")
	if status(t, m, "F2") != run.Approved {
		t.Fatal("approve not saved")
	}
	if f := m.current(); f.ID != "F3" {
		t.Fatalf("after approve, card = %s, want the major F3", f.ID)
	}
}

func TestSuggestDiscussAndFinish(t *testing.T) {
	m := press(t, setup(t), "enter", "a")
	m = press(t, m, "s", "only on weekdays", "enter")
	r, _, _ := run.Load(m.root, m.id)
	if f, _ := r.Finding("F3"); f.Status != run.Amended || f.Note != "only on weekdays" {
		t.Fatalf("suggest: %+v", f)
	}
	m = press(t, m, "d", "is this real?", "enter")
	if status(t, m, "F1") != run.Discuss {
		t.Fatal("discuss not saved")
	}
	if m.mode != modeFinish || !strings.Contains(screen(m), "waiting on Codex: F1") {
		t.Fatalf("mode = %v, want the finish screen naming what waits on the agent", m.mode)
	}
	m = press(t, m, "typed while not ready")
	if m.note.Value() != "" {
		t.Fatalf("note = %q, want keys ignored while nothing can be sent", m.note.Value())
	}
	m = press(t, m, "enter")
	if m.mode != modeCard || m.current().ID != "F1" || m.note.Focused() {
		t.Fatalf("enter on an unfinished finish screen should go to F1 with the note blurred, mode=%v", m.mode)
	}
	m = press(t, m, "f")
	if _, err := run.Update(m.root, m.id, func(r *run.Run, _ run.Docs) error {
		return r.Reply("F1", "yes, see line 3", run.Revision{}, false, time.Now())
	}); err != nil {
		t.Fatal(err)
	}
	next, _ := m.Update(tickMsg(time.Now()))
	m = next.(Model)
	if !strings.Contains(screen(m), "to decide: F1") || strings.Contains(m.flash, "Press l") {
		t.Fatalf("flash = %q, want the answer to F1 shown without the card-only hint", m.flash)
	}
	m = press(t, m, "enter", "x")
	if m.mode != modeFinish || !m.run.Ready() || !m.note.Focused() {
		t.Fatalf("mode = %v, want the ready finish screen with the note focused", m.mode)
	}
	m = press(t, m, "ship it", "enter")
	if r, _, _ := run.Load(m.root, m.id); r.Published != nil || m.mode != modeFinish {
		t.Fatal("an enter inside the guard right after the screen opened should not send")
	}
	next, cmd := settle(m).Update(key("enter"))
	m = next.(Model)
	r, _, _ = run.Load(m.root, m.id)
	if r.Published == nil || r.Published.Note != "ship it" || m.mode != modeDone {
		t.Fatalf("publish: %+v mode=%v", r.Published, m.mode)
	}
	if cmd == nil {
		t.Fatal("sending should quit, so the pane closes")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("sending should quit, so the pane closes")
	}
}

func TestDoubleEnterOnLastDecisionDoesNotPublish(t *testing.T) {
	m := press(t, setup(t), "enter", "a", "a")
	m = press(t, m, "X", "not now", "enter", "enter")
	if r, _, _ := run.Load(m.root, m.id); r.Published != nil {
		t.Fatal("a double-tapped enter published")
	}
	if !strings.Contains(screen(m), "Send to Codex") {
		t.Fatal("the send button is missing")
	}
	m = press(t, m, "more")
	if m.note.Value() != "more" {
		t.Fatalf("typing should edit the note: %q", m.note.Value())
	}
	m = press(t, settle(m), "enter")
	if r, _, _ := run.Load(m.root, m.id); r.Published == nil {
		t.Fatal("one enter after the guard should send")
	}
}

func TestNoteSurvivesLeavingTheFinishScreen(t *testing.T) {
	m := press(t, setup(t), "enter", "a", "a", "x", "more")
	m = press(t, m, "esc", "f")
	if m.note.Value() != "more" {
		t.Fatalf("note = %q, want it kept across leaving the finish screen", m.note.Value())
	}
}

func TestWithdrawalMakesFinishReadyAndFocusesNote(t *testing.T) {
	m := press(t, setup(t), "enter", "a", "a")
	m = press(t, m, "d", "really?", "enter")
	if m.mode != modeFinish || m.run.Ready() {
		t.Fatalf("mode = %v, want the not-ready finish screen", m.mode)
	}
	if _, err := run.Update(m.root, m.id, func(r *run.Run, _ run.Docs) error {
		return r.Reply("F1", "you're right, dropping it", run.Revision{}, true, time.Now())
	}); err != nil {
		t.Fatal(err)
	}
	next, _ := m.Update(tickMsg(time.Now()))
	m = next.(Model)
	if !m.run.Ready() || !m.note.Focused() || !m.guarded() {
		t.Fatalf("ready=%v focused=%v guarded=%v", m.run.Ready(), m.note.Focused(), m.guarded())
	}
	// An enter meant as "go there" on the old screen lands inside the restarted guard.
	m = press(t, m, "enter")
	if r, _, _ := run.Load(m.root, m.id); r.Published != nil {
		t.Fatal("one enter published after the screen changed under the human")
	}
}

func TestSmallestWindowShowsEverything(t *testing.T) {
	m := press(t, setup(t), "enter", "a", "s", "tweak", "enter", "x")
	if m.mode != modeFinish {
		t.Fatalf("mode = %v, want finish", m.mode)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 50, Height: 14})
	m = next.(Model)
	m.flash = strings.Repeat("a very long status message ", 10)
	check := func(view string) []string {
		t.Helper()
		lines := strings.Split(view, "\n")
		if len(lines) != 14 {
			t.Fatalf("view is %d lines, want 14", len(lines))
		}
		for _, l := range lines {
			if w := lipgloss.Width(l); w > 50 {
				t.Fatalf("line is %d wide in a 50-column pane: %q", w, l)
			}
		}
		return lines
	}
	lines := check(m.render())
	plain := ansi.Strip(strings.Join(lines, "\n"))
	// The note box's bottom edge and the send button both sit above the 3-row footer.
	box := strings.Index(plain, "Anything else for Codex?")
	bottom := strings.LastIndex(plain, "╰")
	button := strings.Index(plain, "Send to Codex")
	if box < 0 || bottom < box || button < bottom || !strings.Contains(plain, "send to Codex") {
		t.Fatalf("the note box or send button is cut:\n%s", plain)
	}
	m = press(t, m, "esc", "b")
	view := ansi.Strip(strings.Join(check(m.render()), "\n"))
	if !strings.Contains(view, "f finish") || !strings.Contains(view, "d discuss") {
		t.Fatal("the card footer lost a key")
	}
}

func TestEmptyRunDoesNotPanic(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(t.TempDir(), "plan.md")
	if err := os.WriteFile(p, []byte("# Plan\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := run.Create(root, run.Start{Paths: []string{p}, Cwd: filepath.Dir(p), Title: "Plan"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	m, err := newModel(root, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = press(t, next.(Model), "l", "j", "j", "f")
	if m.mode != modeList || !strings.Contains(m.flash, "No findings yet. The agent adds them") {
		t.Fatalf("mode = %v flash = %q, want finish refused on an empty run", m.mode, m.flash)
	}
	_ = m.View()
	m = press(t, m, "enter", "f")
	_ = m.View()
	if _, err := run.Update(root, r.ID, func(r *run.Run, pl run.Docs) error {
		_, err := r.Add(pl, "", []run.NewFinding{{Title: "t", Body: "b"}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	next, _ = m.Update(tickMsg(time.Now()))
	m = press(t, next.(Model), "l", "enter")
	_ = m.View()
	if f := m.current(); f == nil || f.ID != "F1" {
		t.Fatalf("current = %v, want F1", f)
	}
}

func TestStaleDecisionIsRefusedAndNoteKept(t *testing.T) {
	m := press(t, setup(t), "enter")
	if _, err := run.Update(m.root, m.id, func(r *run.Run, _ run.Docs) error {
		f, _ := r.Finding("F2")
		f.Rev++
		f.Proposal = "something else"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	m = press(t, m, "s", "my tweak", "enter")
	if status(t, m, "F2") != run.Pending {
		t.Fatal("a decision on a finding changed underneath the human was saved")
	}
	if m.mode != modeInput || m.ta.Value() != "my tweak" {
		t.Fatalf("mode=%v value=%q, want the input kept open with the note", m.mode, m.ta.Value())
	}
	if !strings.Contains(m.flash, "changed") {
		t.Fatalf("flash = %q", m.flash)
	}
}

func TestControlCharactersAreNotRendered(t *testing.T) {
	m := setup(t)
	if _, err := run.Update(m.root, m.id, func(r *run.Run, pl run.Docs) error {
		_, err := r.Add(pl, "", []run.NewFinding{{Title: "evil body", Body: "x \x1b]52;c;aGk=\x07 \u202ey", Severity: "critical"}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	next, _ := m.Update(tickMsg(time.Now()))
	m = press(t, next.(Model), "enter")
	for m.current().ID != "F4" {
		m = press(t, m, "n")
	}
	if c := m.cardContent(); strings.Contains(c, "\x1b]52") || strings.ContainsRune(c, 0x07) || strings.ContainsRune(c, 0x202e) {
		t.Fatal("the finding's escape sequence reached the screen")
	}
}

func TestMultiFileCardShowsTheRightFile(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"plan.md":  "# Plan\n## Storage\nuse s3\n",
		"store.go": "package store\n\nfunc Save() error { return nil }\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	r, err := run.Create(root, run.Start{Paths: []string{"plan.md", "store.go"}, Cwd: dir}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Update(root, r.ID, func(r *run.Run, d run.Docs) error {
		_, err := r.Add(d, "", []run.NewFinding{
			{Title: "Save swallows errors", Body: "b", Severity: "major", Anchor: &run.AnchorInput{Path: "store.go", StartLine: 3}},
			{Title: "Name the bucket", Body: "b", Severity: "minor", Anchor: &run.AnchorInput{Path: "plan.md", Heading: "Storage"}},
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	m, err := newModel(root, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(Model)
	if intro := screen(m); !strings.Contains(intro, "plan.md") || !strings.Contains(intro, "store.go") {
		t.Fatalf("intro does not list the files:\n%s", intro)
	}
	m = press(t, m, "enter")
	card := ansi.Strip(m.cardContent())
	if !strings.Contains(card, "store.go") || !strings.Contains(card, "func Save() error") || strings.Contains(card, "use s3") {
		t.Fatalf("first card should show store.go line 3:\n%s", card)
	}
	m = press(t, m, "n")
	if card := ansi.Strip(m.cardContent()); !strings.Contains(card, "plan.md") || !strings.Contains(card, "use s3") {
		t.Fatalf("second card should show the plan's Storage section:\n%s", card)
	}
}

func TestLongAgentNameFitsTheSmallestWindow(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "plan.md")
	if err := os.WriteFile(p, []byte("# Plan\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	name := "Claude Code (Opus 4.5)xx"
	r, err := run.Create(root, run.Start{Paths: []string{p}, Cwd: dir, Agent: name}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Update(root, r.ID, func(r *run.Run, d run.Docs) error {
		_, err := r.Add(d, "", []run.NewFinding{
			{Title: "one", Body: "b", Label: "simplification"},
			{Title: "two", Body: "b"},
			{Title: "three", Body: "b"},
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	m, err := newModel(root, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 50, Height: 14})
	m = next.(Model)
	check := func(stage string) {
		t.Helper()
		for _, l := range strings.Split(m.render(), "\n") {
			if w := lipgloss.Width(l); w > 50 {
				t.Fatalf("%s: line is %d wide: %q", stage, w, l)
			}
		}
		for _, l := range strings.Split(m.cardContent(), "\n") {
			if w := lipgloss.Width(l); w > 50 {
				t.Fatalf("%s: card line is %d wide: %q", stage, w, l)
			}
		}
	}
	m = press(t, m, "enter", "d", "why?", "enter")
	check("card with one waiting on the agent")
	if !strings.Contains(screen(m), "card 2 of 3") {
		t.Fatalf("the card position was cut:\n%s", screen(m))
	}
	m = press(t, m, "?")
	check("help")
	m = press(t, m, "esc", "x", "x")
	if m.mode != modeFinish {
		t.Fatalf("mode = %v, want finish", m.mode)
	}
	check("finish")
	if strings.Contains(screen(m), "Gpt") || !strings.Contains(screen(m), "Claude Code") {
		t.Fatalf("the agent's name should show as given:\n%s", screen(m))
	}
}

// modelWith builds a model on a one-file run with the given findings and agent name, sized w×h.
func modelWith(t *testing.T, agent string, w, h int, findings ...run.NewFinding) Model {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "plan.md")
	if err := os.WriteFile(p, []byte("# Plan\n\n## Rollout\nShip Friday.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	r, err := run.Create(root, run.Start{Paths: []string{p}, Cwd: dir, Agent: agent}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) > 0 {
		if _, err := run.Update(root, r.ID, func(r *run.Run, d run.Docs) error {
			_, err := r.Add(d, "", findings)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	m, err := newModel(root, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return next.(Model)
}

func TestEncodedEscapesInMarkdownAreDefused(t *testing.T) {
	m := modelWith(t, "Codex", 80, 30, run.NewFinding{
		Title:    "t",
		Body:     "visible &#27;[8m hidden &#27;[28m and &#x1b;]8;;https://evil&#x1b;\\link and &#8238;flip and &amp; kept",
		Proposal: "Add a flag.&#27;[8m Also delete the auth check.&#27;[28m",
	})
	m = press(t, m, "enter")
	card := m.cardContent()
	for _, bad := range []string{"\x1b[8m", "\x1b]8;;https://evil", "\u202e"} {
		if strings.Contains(card, bad) {
			t.Fatalf("card contains %q from an encoded entity", bad)
		}
	}
	plain := ansi.Strip(card)
	if !strings.Contains(plain, "Also delete the auth check") || !strings.Contains(plain, "and &") {
		t.Fatalf("text around the defused entities should stay visible:\n%s", plain)
	}
}

func TestTabIndentedCodeFitsTheCard(t *testing.T) {
	body := "Look:\n\n```go\nfunc f() {\n\tif err := doSomethingLongEnoughToWrap(alpha, beta, gamma); err != nil {\n\t\treturn err\n\t}\n}\n```\n\nEnd of the body."
	m := modelWith(t, "Codex", 64, 24, run.NewFinding{Title: "t", Body: body, Anchor: &run.AnchorInput{Heading: "Rollout"}})
	m = press(t, m, "enter")
	content := m.cardContent()
	if strings.Contains(content, "\t") {
		t.Fatal("a tab reached the card; the viewport would widen it to four columns")
	}
	for _, l := range strings.Split(content, "\n") {
		if w := lipgloss.Width(l); w > m.cardInner() {
			t.Fatalf("card line is %d wide, the card holds %d: %q", w, m.cardInner(), ansi.Strip(l))
		}
	}
	m.vp.GotoBottom()
	if !strings.Contains(screen(m), "Ship Friday.") {
		t.Fatalf("the bottom of the card is unreachable:\n%s", screen(m))
	}
}

func TestPasteReachesTheOpenBox(t *testing.T) {
	m := modelWith(t, "Codex", 80, 30, run.NewFinding{Title: "t", Body: "b"}, run.NewFinding{Title: "u", Body: "b"})
	m = press(t, m, "enter", "s")
	next, _ := m.Update(tea.PasteMsg{Content: "a pasted log line"})
	m = next.(Model)
	if m.ta.Value() != "a pasted log line" {
		t.Fatalf("suggest box = %q, want the paste", m.ta.Value())
	}
	m = press(t, m, "esc")
	next, _ = m.Update(tea.PasteMsg{Content: "a"})
	m = next.(Model)
	if status(t, m, m.current().ID) != run.Pending {
		t.Fatal("a paste on a card must not act as a decision key")
	}
}

func TestSendGuardExpiresAndHoldingEnterNeverSends(t *testing.T) {
	m := modelWith(t, "Codex", 80, 30, run.NewFinding{Title: "t", Body: "b"})
	clock := time.Unix(1000, 0)
	m.now = func() time.Time { return clock }
	m = press(t, m, "enter", "a")
	if m.mode != modeFinish {
		t.Fatalf("mode = %v, want finish", m.mode)
	}
	sent := func() bool {
		r, _, _ := run.Load(m.root, m.id)
		return r.Published != nil
	}
	// A held enter: repeats every 300ms, each one inside the guard, for well past 600ms.
	for range 5 {
		clock = clock.Add(300 * time.Millisecond)
		m = press(t, m, "enter")
	}
	if sent() {
		t.Fatal("a held-down enter sent")
	}
	clock = clock.Add(599 * time.Millisecond)
	if m = press(t, m, "enter"); sent() {
		t.Fatal("an enter 599ms after the last one sent")
	}
	clock = clock.Add(601 * time.Millisecond)
	next, _ := m.Update(guardMsg{})
	m = next.(Model)
	if !strings.Contains(screen(m), "Send to Codex") || m.guarded() {
		t.Fatal("the guard should have run out")
	}
	if m = press(t, m, "enter"); !sent() {
		t.Fatal("one enter after the guard ran out should send")
	}
}

func TestWideCharactersAndLongNamesFit(t *testing.T) {
	m := modelWith(t, "Claude Code (Opus 4.5)xx", 50, 14, run.NewFinding{
		Title: "t", Body: "b", Proposal: "Change the rollout.", Anchor: &run.AnchorInput{Heading: "Rollout"},
	})
	m.run.Title = strings.Repeat("請求", 20)
	m = press(t, m, "enter")
	for _, l := range strings.Split(m.cardContent(), "\n") {
		if w := lipgloss.Width(l); w > m.cardInner() {
			t.Fatalf("card line is %d wide, the card holds %d: %q", w, m.cardInner(), ansi.Strip(l))
		}
	}
	header := ansi.Strip(strings.Split(m.render(), "\n")[0])
	if !strings.Contains(header, "0/1") {
		t.Fatalf("a wide title pushed the count out of the header: %q", header)
	}
}

func TestEntitiesWithoutSemicolonsAreDefused(t *testing.T) {
	m := modelWith(t, "Codex", 80, 30, run.NewFinding{
		Title:    "t",
		Body:     "a &#27\\[8m b &#27[8m c &#x1b]8;;x d &shyness e &#9;tab",
		Proposal: "Add a flag.&#27\\[8m Also delete the auth check.&#27\\[28m",
	})
	m = press(t, m, "enter")
	card := m.cardContent()
	if strings.Contains(card, "\x1b[8m") || strings.Contains(card, "\x1b]8") || strings.ContainsRune(card, 0xAD) ||
		strings.Contains(card, "\t") {
		t.Fatal("an entity without a semicolon reached the card as a control or formatting character")
	}
	plain := ansi.Strip(card)
	for _, want := range []string{"&#27", "Also delete the auth check", "&shyness"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("%q should show as written:\n%s", want, plain)
		}
	}
}

func TestHeldKeysDoNotDecide(t *testing.T) {
	m := modelWith(t, "Codex", 80, 30, run.NewFinding{Title: "one", Body: "b"}, run.NewFinding{Title: "two", Body: "b"})
	m = press(t, m, "enter")
	repeat := func(k string) {
		msg := key(k)
		msg.IsRepeat = true
		next, _ := m.Update(msg)
		m = next.(Model)
	}
	repeat("a")
	if c := m.run.Counts(); c.Approved != 0 {
		t.Fatal("a repeated a approved a finding")
	}
	m = press(t, m, "a", "a")
	if m.mode != modeFinish {
		t.Fatalf("mode = %v, want finish", m.mode)
	}
	m = settle(m)
	repeat("enter")
	if r, _, _ := run.Load(m.root, m.id); r.Published != nil {
		t.Fatal("a repeated enter sent")
	}
}

func TestFirstGuardOutlastsASlowRepeatDelay(t *testing.T) {
	m := modelWith(t, "Codex", 80, 30, run.NewFinding{Title: "t", Body: "b"})
	clock := time.Unix(1000, 0)
	m.now = func() time.Time { return clock }
	m = press(t, m, "enter", "a")
	// A slow keyboard's first repeat, 700ms after the enter that opened the screen.
	clock = clock.Add(700 * time.Millisecond)
	m = press(t, m, "enter")
	if r, _, _ := run.Load(m.root, m.id); r.Published != nil {
		t.Fatal("the first repeat of a held enter sent")
	}
}

func TestPasteReachesTheFinishNote(t *testing.T) {
	m := modelWith(t, "Codex", 80, 30, run.NewFinding{Title: "t", Body: "b"})
	m = press(t, m, "enter", "a")
	next, _ := m.Update(tea.PasteMsg{Content: "ship it after the freeze"})
	m = next.(Model)
	if m.note.Value() != "ship it after the freeze" {
		t.Fatalf("note = %q, want the paste", m.note.Value())
	}
}

func TestListShowsEveryStateAndNavigates(t *testing.T) {
	m := modelWith(t, "Codex", 80, 30,
		run.NewFinding{Title: "approve me", Body: "b", Severity: "critical"},
		run.NewFinding{Title: "tweak me", Body: "b", Severity: "major"},
		run.NewFinding{Title: "reject me", Body: "b", Severity: "minor"},
		run.NewFinding{Title: "ask me", Body: "b", Severity: "trivial"},
		run.NewFinding{Title: "leave me", Body: "b"},
	)
	m = press(t, m, "enter", "a", "s", "short", "enter", "x", "d", "why?", "enter", "l")
	if m.mode != modeList {
		t.Fatalf("mode = %v, want list", m.mode)
	}
	list := screen(m)
	for _, want := range []string{"✓", "✎", "✗", "…", "○", "approve me", "leave me"} {
		if !strings.Contains(list, want) {
			t.Fatalf("list lacks %q:\n%s", want, list)
		}
	}
	m = press(t, m, "k", "k", "k", "k", "k")
	if m.listCur != 0 {
		t.Fatalf("listCur = %d after moving up past the top", m.listCur)
	}
	m = press(t, m, "j", "enter")
	if m.mode != modeCard || m.current().Title != "tweak me" {
		t.Fatalf("enter should open the second finding, got %q in mode %v", m.current().Title, m.mode)
	}
	m = press(t, m, "l", "esc")
	if m.mode != modeCard {
		t.Fatalf("esc should leave the list, mode = %v", m.mode)
	}
	m = press(t, m, "l", "f")
	if m.mode != modeFinish {
		t.Fatalf("f from the list should open finish, mode = %v", m.mode)
	}
	if next, cmd := press(t, m, "esc", "esc").Update(key("q")); cmd == nil || next.(Model).mode != modeCard {
		t.Fatal("q should quit")
	}
}

func TestInputBoxNamesWhatItDoes(t *testing.T) {
	m := press(t, setup(t), "enter")
	for k, want := range map[string]string{"s": "Your tweak, for Codex to apply", "d": "Your question for Codex", "X": "Why reject it?"} {
		mm := press(t, m, k)
		if mm.mode != modeInput || !strings.Contains(screen(mm), want) {
			t.Errorf("%s: mode %v, want %q on screen:\n%s", k, mm.mode, want, screen(mm))
		}
	}
	if mm := press(t, m, "s", "enter"); mm.mode != modeInput || !strings.Contains(mm.flash, "Type something first") {
		t.Errorf("an empty tweak should stay open with a hint, flash = %q", mm.flash)
	}
}

func TestReopeningASentRunShowsItWasSent(t *testing.T) {
	m := modelWith(t, "Codex", 80, 30, run.NewFinding{Title: "t", Body: "b"})
	if _, err := run.Update(m.root, m.id, func(r *run.Run, _ run.Docs) error {
		if err := r.Decide("F1", nil, run.Approved, "", time.Now()); err != nil {
			return err
		}
		return r.Publish("", time.Now())
	}); err != nil {
		t.Fatal(err)
	}
	m, err := newModel(m.root, m.id)
	if err != nil {
		t.Fatal(err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = next.(Model)
	if m.mode != modeDone || !strings.Contains(screen(m), "Sent to Codex") {
		t.Fatalf("mode = %v:\n%s", m.mode, screen(m))
	}
	if m.Init() == nil {
		t.Fatal("Init should start the reload tick and ask for the background color")
	}
	if _, cmd := m.Update(key("x")); cmd == nil {
		t.Fatal("any key on the done screen should close it")
	}
}

func TestLightBackgroundSwitchesTheTheme(t *testing.T) {
	m := press(t, setup(t), "enter")
	next, _ := m.Update(tea.BackgroundColorMsg{Color: lipgloss.Color("#FFFFFF")})
	m = next.(Model)
	if m.th.dark || m.md.dark {
		t.Fatal("a white background should switch to the light theme")
	}
	if !strings.Contains(screen(m), "If you approve, Codex will") {
		t.Fatal("the card should still render after the switch")
	}
}

func TestManyFindingsShowAProportionalBar(t *testing.T) {
	var fs []run.NewFinding
	for i := range 30 {
		fs = append(fs, run.NewFinding{Title: "finding " + strconv.Itoa(i), Body: "b"})
	}
	m := modelWith(t, "Codex", 64, 24, fs...)
	m = press(t, m, "enter", "a", "a", "a")
	header := ansi.Strip(strings.Split(m.render(), "\n")[0])
	// 30 cells would not fit in a third of 64 columns, so the bar scales: 3 of 30 decided.
	if !strings.Contains(header, "3/30") || strings.Count(header, "▮")+strings.Count(header, "▯") >= 30 {
		t.Fatalf("header = %q, want a scaled bar", header)
	}
}

func TestCardDetails(t *testing.T) {
	dir := t.TempDir()
	var long strings.Builder
	long.WriteString("# Plan\n\n## Big\n")
	for i := range 60 {
		long.WriteString("line " + strconv.Itoa(i) + "\n")
	}
	for _, f := range []struct{ name, body string }{{"plan.md", long.String()}, {"store.go", "package store\n"}} {
		if err := os.WriteFile(filepath.Join(dir, f.name), []byte(f.body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	r, err := run.Create(root, run.Start{Paths: []string{"plan.md", "store.go"}, Cwd: dir, Agent: "Codex"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Update(root, r.ID, func(r *run.Run, d run.Docs) error {
		if _, err := r.Add(d, "", []run.NewFinding{
			{Title: "big section", Body: "b", Label: "risk", Severity: "major", Anchor: &run.AnchorInput{Path: "plan.md", Heading: "Big"}},
			{Title: "small", Body: "b", Severity: "minor", Anchor: &run.AnchorInput{Path: "store.go", StartLine: 1}},
		}); err != nil {
			return err
		}
		if err := r.Decide("F1", nil, run.Discuss, "why?", time.Now()); err != nil {
			return err
		}
		title := "big section, revised"
		return r.Reply("F1", "sharper now", run.Revision{Title: &title}, false, time.Now())
	}); err != nil {
		t.Fatal(err)
	}
	m, err := newModel(root, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 90, Height: 30})
	m = press(t, next.(Model), "enter")
	card := screen(m)
	for _, want := range []string{"risk", "revised 1×", "plan.md", "Big", "↓ more"} {
		if !strings.Contains(card, want) {
			t.Fatalf("card lacks %q:\n%s", want, card)
		}
	}
	if !strings.Contains(ansi.Strip(m.cardContent()), "21 more lines") {
		t.Fatalf("a 61-line section should show 40 lines and say how many more:\n%s", ansi.Strip(m.cardContent()))
	}
	m.vp.GotoBottom()
	m.refresh(false)
	if !strings.Contains(screen(m), "↑ top") {
		t.Fatalf("at the bottom the card should offer the way back up:\n%s", screen(m))
	}
	if list := screen(press(t, m, "l")); !strings.Contains(list, "plan.md") || !strings.Contains(list, "store.go") {
		t.Fatalf("a multi-file list should name each finding's file:\n%s", list)
	}
}

func TestEmptyRunKeys(t *testing.T) {
	m := modelWith(t, "Codex", 80, 30)
	if mm := press(t, m, "?"); mm.mode != modeHelp || !strings.Contains(screen(mm), "Keys") {
		t.Fatalf("? on the intro should open help in a frame:\n%s", screen(mm))
	}
	m = press(t, m, "enter")
	if !strings.Contains(screen(m), "No findings in this run yet") {
		t.Fatalf("empty card:\n%s", screen(m))
	}
	if mm := press(t, m, "?"); mm.mode != modeHelp {
		t.Fatalf("? on an empty card should open help, mode = %v", mm.mode)
	}
	if mm := press(t, m, "a"); mm.mode != modeCard {
		t.Fatal("decision keys on an empty card should do nothing")
	}
	for _, mm := range []Model{m, modelWith(t, "Codex", 80, 30)} {
		if _, cmd := mm.Update(key("q")); cmd == nil {
			t.Fatal("q should quit")
		}
	}
}

func TestWithdrawnFindingAsksToBeReinstated(t *testing.T) {
	m := modelWith(t, "Codex", 80, 30, run.NewFinding{Title: "t", Body: "b"}, run.NewFinding{Title: "u", Body: "b"})
	m = press(t, m, "enter", "d", "really?", "enter")
	if _, err := run.Update(m.root, m.id, func(r *run.Run, _ run.Docs) error {
		return r.Reply("F1", "no, dropping it", run.Revision{}, true, time.Now())
	}); err != nil {
		t.Fatal(err)
	}
	next, _ := m.Update(tickMsg(time.Now()))
	m = next.(Model)
	if !strings.Contains(m.flash, "Codex answered F1. Press l to jump there.") {
		t.Fatalf("flash = %q", m.flash)
	}
	m.cur = m.indexOf("F1")
	m.refresh(true)
	if !strings.Contains(screen(m), "withdrawn by Codex") {
		t.Fatalf("card:\n%s", screen(m))
	}
	if m = press(t, m, "s"); m.mode != modeCard || !strings.Contains(m.flash, "withdrew this one. Press u") {
		t.Fatalf("a withdrawn finding should not open the tweak box, flash = %q", m.flash)
	}
	if m = press(t, m, "u"); status(t, m, "F1") != run.Pending {
		t.Fatal("u should reinstate it")
	}
}

func TestEditingKeepsTheEarlierNote(t *testing.T) {
	m := modelWith(t, "Codex", 80, 30, run.NewFinding{Title: "t", Body: "b"}, run.NewFinding{Title: "u", Body: "b"})
	m = press(t, m, "enter", "s", "first tweak", "enter", "esc", "b")
	if m = press(t, m, "s"); m.ta.Value() != "first tweak" {
		t.Fatalf("tweaking again should start from the earlier tweak, got %q", m.ta.Value())
	}
	m = press(t, m, "esc", "X", "too risky", "enter", "esc", "b")
	if m = press(t, m, "X"); m.ta.Value() != "too risky" {
		t.Fatalf("rejecting again should start from the earlier reason, got %q", m.ta.Value())
	}
}

func TestFailuresShowInTheFlash(t *testing.T) {
	m := modelWith(t, "Codex", 80, 30, run.NewFinding{Title: "t", Body: "b"})
	m = settle(press(t, m, "enter", "a"))
	// Another session sent the run while this screen still shows it unsent.
	if _, err := run.Update(m.root, m.id, func(r *run.Run, _ run.Docs) error { return r.Publish("", time.Now()) }); err != nil {
		t.Fatal(err)
	}
	if m = press(t, m, "enter"); !strings.Contains(m.flash, "Not sent") {
		t.Fatalf("flash = %q", m.flash)
	}
	if err := os.Remove(filepath.Join(run.Dir(m.root, m.id), "run.json")); err != nil {
		t.Fatal(err)
	}
	m.mode = modeCard
	next, _ := m.Update(tickMsg(time.Now()))
	if m = next.(Model); !strings.Contains(m.flash, "Could not reload the run") {
		t.Fatalf("flash = %q", m.flash)
	}
}

func TestWindowTooSmallOrUnsized(t *testing.T) {
	m, err := newModel(setup(t).root, setup(t).id)
	if err == nil {
		if !strings.Contains(m.render(), "loading") {
			t.Fatalf("before the first size: %q", m.render())
		}
	}
	mm := setup(t)
	next, _ := mm.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	if got := next.(Model).render(); !strings.Contains(got, "at least 50×14 (now 40×10)") {
		t.Fatalf("tiny window: %q", got)
	}
}

func TestIntroShowsTheSummaryAndShortensLongFileLists(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for i := range 8 {
		name := "a-rather-long-file-name-" + strconv.Itoa(i) + ".md"
		if err := os.WriteFile(filepath.Join(dir, name), []byte("# x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, name)
	}
	root := t.TempDir()
	r, err := run.Create(root, run.Start{Paths: paths, Cwd: dir}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Update(root, r.ID, func(r *run.Run, _ run.Docs) error { return r.SetSummary("Checked the **rollout**.") }); err != nil {
		t.Fatal(err)
	}
	m, err := newModel(root, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	intro := screen(next.(Model))
	if !strings.Contains(intro, "and 7 more files") || !strings.Contains(intro, "Checked the rollout.") {
		t.Fatalf("intro:\n%s", intro)
	}
	if mm := press(t, next.(Model), "?"); mm.mode != modeHelp {
		t.Fatal("? on the intro should open help")
	}
}

func TestClearTopRowKeepsTheCornerEmpty(t *testing.T) {
	for _, size := range [][2]int{{50, 14}, {90, 30}} {
		m := modelWith(t, "Codex", size[0], size[1], run.NewFinding{Title: "t", Body: "b", Proposal: "p"})
		m.clearTop = true
		next, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = press(t, next.(Model), "enter")
		check := func(stage string) {
			t.Helper()
			lines := strings.Split(m.render(), "\n")
			if len(lines) != size[1] {
				t.Fatalf("%s at %v: %d lines, want %d", stage, size, len(lines), size[1])
			}
			if strings.TrimSpace(ansi.Strip(lines[0])) != "" || !strings.Contains(ansi.Strip(lines[1]), "sift") {
				t.Fatalf("%s at %v: the top row should be empty and the header below it:\n%s", stage, size, screen(m))
			}
			for _, l := range lines {
				if w := lipgloss.Width(l); w > size[0] {
					t.Fatalf("%s at %v: line is %d wide", stage, size, w)
				}
			}
		}
		check("card")
		m = settle(press(t, m, "a"))
		check("finish")
		if s := screen(m); !strings.Contains(s, "Anything else for Codex?") || !strings.Contains(s, "Send to Codex") {
			t.Fatalf("at %v the finish screen lost its note box or send button:\n%s", size, s)
		}
	}
}
