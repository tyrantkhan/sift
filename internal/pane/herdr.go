package pane

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
)

type herdr struct{ path, pane string }

type herdrPane struct {
	PaneID string `json:"pane_id"`
	Rect   struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	} `json:"rect"`
}

func (herdr) Name() string { return "herdr" }

// Open reads the agent pane's size from the layout first. That step opens nothing, so a failure there leaves no
// stray pane, and Herdr reports the pane's size where the agent's shell has no terminal to ask.
func (h herdr) Open(ctx context.Context, argv []string) error {
	out, err := runCmd(ctx, h.path, "pane", "layout", "--pane", h.pane)
	if err != nil {
		return err
	}
	var layout struct {
		Result struct {
			Layout struct {
				Panes []herdrPane `json:"panes"`
			} `json:"layout"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &layout); err != nil {
		return fmt.Errorf("herdr: unreadable pane layout: %w", err)
	}
	panes := layout.Result.Layout.Panes
	i := slices.IndexFunc(panes, func(p herdrPane) bool { return p.PaneID == h.pane })
	if i < 0 {
		return fmt.Errorf("herdr: the layout does not list pane %s", h.pane)
	}
	direction := "right"
	if below(panes[i].Rect.Width, panes[i].Rect.Height) {
		direction = "down"
	}

	out, err = runCmd(ctx, h.path, "pane", "split", "--pane", h.pane, "--direction", direction, "--focus")
	if err != nil {
		return err
	}
	var split struct {
		Result struct {
			Pane struct {
				PaneID string `json:"pane_id"`
			} `json:"pane"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &split); err != nil || split.Result.Pane.PaneID == "" {
		return fmt.Errorf("herdr: the split did not report its new pane")
	}
	_, err = runCmd(ctx, h.path, "pane", "run", split.Result.Pane.PaneID, typedLine(argv))
	return err
}
