# Changelog

## [0.1.4](https://github.com/tyrantkhan/sift/compare/v0.1.3...v0.1.4) (2026-10-25)


### Bug Fixes

* keep Orca's pane controls off the header with a gutter, not a row ([#20](https://github.com/tyrantkhan/sift/issues/20)) ([f8e2b66](https://github.com/tyrantkhan/sift/commit/f8e2b6605db7ca72236001ea4f9bf079e657f218))

## [0.1.3](https://github.com/tyrantkhan/sift/compare/v0.1.2...v0.1.3) (2026-10-25)


### Features

* tell the agent to use sift from a SessionStart hook ([#18](https://github.com/tyrantkhan/sift/issues/18)) ([caa3db2](https://github.com/tyrantkhan/sift/commit/caa3db23c534dc72412bf8e0bdc6b2d46ad886f1))

## [0.1.2](https://github.com/tyrantkhan/sift/compare/v0.1.1...v0.1.2) (2026-10-25)


### Bug Fixes

* show the conversation above the code on a finding's card ([#15](https://github.com/tyrantkhan/sift/issues/15)) ([e4487bd](https://github.com/tyrantkhan/sift/commit/e4487bdeb7e4f2a4672072f4b0c4a26974997f5c))

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
