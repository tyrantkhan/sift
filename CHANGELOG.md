# Changelog

## [0.1.1](https://github.com/tyrantkhan/sift/compare/v0.1.0...v0.1.1) (2026-10-25)


### Bug Fixes

* keep Orca's pane controls off the review's progress ([#12](https://github.com/tyrantkhan/sift/issues/12)) ([c6e361e](https://github.com/tyrantkhan/sift/commit/c6e361eac49e2098cf0b56cb18d5d63ba9c9f368))

## 0.1.0 (2026-10-24)

First release. sift takes the findings an agent produces when it reviews a plan, a spec or code, shows them to you one at a time, and hands your decisions back to the agent to apply.

### Features

* **One card at a time.** `sift review` shows each finding with the change the agent proposes and the lines it points at, most severe first. Approve, approve with your tweak, reject, or ask the agent about it; then send every decision back in one step.
* **Plans, specs or code.** Review named files, or `sift start --changed` for the code changed on a branch. Findings point at line ranges, or at headings in Markdown, and code excerpts are syntax-highlighted.
* **A loop the agent drives.** `start`, `add`, `summary`, `handoff`, `wait`, `reply` and `feedback` each print one JSON object. `handoff` opens the review in a pane beside the agent in [Herdr](https://herdr.dev), tmux, Orca or iTerm2 ([#5](https://github.com/tyrantkhan/sift/issues/5)).
* **Any agent.** One `sift` skill, installable in Claude Code, Codex, Cursor, Gemini CLI and Pi, or any tool that reads Agent Skills.
* **Signed builds.** macOS binaries are signed with a Developer ID and notarized by Apple; install with `brew install --cask tyrantkhan/tap/sift`.
