package tui

import (
	"image/color"

	"charm.land/lipgloss/v2"

	"github.com/tyrantkhan/sift/internal/run"
)

// theme is the logo's palette resolved for the terminal's background. Fills (the header bar, pills, the send
// button) use fixed colors with white text, which read on light and dark alike; text colors adapt.
type theme struct {
	dark bool

	indigo, violet, lilac color.Color
	white                 color.Color
	accent, muted, faint  color.Color
	good, tweak, bad      color.Color
	talk, warn            color.Color
}

func newTheme(dark bool) theme {
	ld := lipgloss.LightDark(dark)
	c := lipgloss.Color
	return theme{
		dark:   dark,
		indigo: c("#3328A9"),
		violet: c("#5A4FCF"),
		lilac:  c("#A79BFF"),
		white:  c("#FFFFFF"),
		accent: ld(c("#5A4FCF"), c("#A79BFF")),
		muted:  ld(c("#6B6880"), c("#8E8CA8")),
		faint:  ld(c("#C9C5EE"), c("#3D3873")),
		good:   ld(c("#1F8A4C"), c("#5BD08D")),
		tweak:  ld(c("#1C7C99"), c("#5CC8E8")),
		bad:    ld(c("#C23B3B"), c("#FF7A7A")),
		talk:   ld(c("#A5408F"), c("#E58AD3")),
		warn:   ld(c("#B5650D"), c("#F2A65A")),
	}
}

func (t theme) fg(c color.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }

func (t theme) severity(s string) color.Color {
	switch s {
	case "critical":
		return lipgloss.Color("#D93A3F")
	case "major":
		return lipgloss.Color("#E07A2E")
	case "minor":
		return lipgloss.Color("#3F7FE0")
	case "trivial":
		return lipgloss.Color("#7D7A99")
	}
	return lipgloss.Color("#5F5C7A")
}

// pill is a lowercase word on a filled background.
func (t theme) pill(text string, bg color.Color) string {
	return lipgloss.NewStyle().Foreground(t.white).Background(bg).Bold(true).Padding(0, 1).Render(text)
}

func (t theme) status(s run.Status) (glyph string, c color.Color) {
	switch s {
	case run.Approved:
		return "✓", t.good
	case run.Amended:
		return "✎", t.tweak
	case run.Rejected:
		return "✗", t.bad
	case run.Discuss:
		return "…", t.talk
	case run.Withdrawn:
		return "–", t.muted
	}
	return "○", t.muted
}

// statusText names a finding's state the way the card's border shows it.
func (t theme) statusText(s run.Status, agent string) string {
	g, c := t.status(s)
	text := map[run.Status]string{
		run.Approved:  "approved",
		run.Amended:   "approved with your tweak",
		run.Rejected:  "rejected",
		run.Discuss:   "waiting on " + agent,
		run.Withdrawn: "withdrawn by " + agent,
	}[s]
	if text == "" {
		text = "to decide"
	}
	return t.fg(c).Render(g + " " + text)
}

// key renders a key hint: the key in bold accent, then what it does. No fill, so the hints stay quieter than the
// card above them.
func (t theme) key(k, label string) string {
	return t.fg(t.accent).Bold(true).Render(k) + " " + t.fg(t.muted).Render(label)
}
