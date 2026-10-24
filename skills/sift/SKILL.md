---
name: sift
description: Use when you have review findings for the human to decide, more than a handful, about a plan, a spec or code, or when the human asks to go through findings "with sift", "one at a time", or "so I can approve/reject each". Files the findings into sift, opens sift review beside you so the human approves, tweaks, rejects or questions each one, answers their questions, then applies exactly what they decided. Brings no review method of its own and never decides for the human.
---

# Hand review findings to the human with sift

`sift` keeps one review round on disk: the files you reviewed and your findings about them. The files can be a plan, a spec, code, or any mix. The human decides each finding, alone on screen, with one key. You answer the ones they send back, they publish, and you apply their decisions.

This workflow works for any agent that can run shell commands. It does not depend on a particular agent or host.

Every agent command (`start`, `add`, `summary`, `handoff`, `wait`, `reply`, `feedback`) prints exactly one JSON object on stdout. A refusal has `"ok": false` and an `error` with `code`, `message` and `fix`. You SHOULD follow `error.fix`. Run `sift help <command>` for any input or result shape not spelled out here.

## Rules

- You MUST NOT run `sift review`, open it by any route other than `sift handoff`, or allocate a pseudo-terminal (`script`, `expect`, `unbuffer`) to reach it. Publishing is the human's alone.
- You MUST NOT send keys to, read, resize or close the review pane.
- You MUST NOT edit files under sift's data directory. Change findings only with `sift add` and `sift reply`.
- You MUST NOT treat a `timeout` from `sift wait` as approval, and MUST NOT make any finding's change before the run is published.
- You MUST NOT apply a rejected or withdrawn finding.
- If a sandbox stops `sift` from writing its data directory (`error.code` is `data-root`), rerun the command with your host's approval to run outside the sandbox. You MUST NOT work around it by setting `SIFT_HOME`.

## 1. Start the round

Name the files you will review, or let git name them:

- `sift start <file>... --title "<short name>" --agent "<your name>"` for a plan, a spec, or chosen files. If your host keeps plans in a directory of its own, pass the path of the plan you wrote there.
- `sift start --changed [--base <branch>] --agent "<your name>"` for the code changed in the current git repository: staged, unstaged and untracked, plus the branch's own commits when `--base` names the branch the work started from, such as `--base main`.

`--agent` is the name the human knows you by, such as Claude or Codex; the review shows it wherever it refers to you.

Keep `run` from the result and pass `--run <run>` to every later command. `files` lists each file's `path`, which you use in anchors, and the `headings` of Markdown files. `skipped` lists changed files left out as deleted, empty or binary; mention them in your summary if they matter.

## 2. Review

Use whatever review method the human asked for, or your own. Write each finding as one decision the human can make in seconds:

- `title`: the change or the problem, in at most 120 characters. Prefer a decision ("Return the error from Save instead of logging it") over a topic ("Error handling").
- `body`: why it matters, in a few short lines. The human reads this on a single card; long bodies defeat the point.
- `proposal`: the concrete change you will make if approved. A finding without one is a question, and SHOULD carry `"label": "question"`.
- `severity`: `critical` (outage, data loss, security), `major` (a real defect on a normal path), `minor` (an edge case or a cost paid later), `trivial` (naming, wording, style). Omit it rather than guess.
- `label`: one word, such as `bug`, `risk`, `gap`, `question`, `simplification`, `sequencing`.
- `anchor`: where the finding points, so the card shows those lines. `{"path": "<file>", "startLine": n, "endLine": m}` works in any file; `{"path": "<file>", "heading": "<section>"}` works in Markdown files and covers the whole section. `path` SHOULD be written exactly as `sift start` printed it; a path that could mean two files is refused. It MAY be left out when the run has one file. Omit `anchor` for a finding about the work as a whole.

One finding MUST hold one decision. You MUST NOT bundle unrelated changes, because the human cannot approve half of one.

## 3. File the findings and a summary

Write the findings to a JSON array in a temporary file and run `sift add --from <file> --run <run>`. A batch is stored entirely or not at all; on `invalid-anchor`, `error.fix` lists the valid paths or headings.

Then run `sift summary --run <run> --text "<2-4 sentences>"`: what you reviewed, the shape of what you found, and what you could not check.

## 4. Hand off

Run `sift handoff --run <run>`. It opens the review in a pane beside yours (tmux, Orca or iTerm2).

- On `no-pane-host` or `pane-failed`, tell the human to run the command in `error.fix` in another terminal.
- On `review-open`, tell the human their review is already open, and go to step 5.

Tell the human in one line that the review is open and how many findings it holds.

## 5. Wait and answer

Run `sift wait --run <run>`. It returns after 9 minutes at most. If your shell tool caps how long one command may run, give this call the longest timeout it allows, and when that cap is under 10 minutes pass `--timeout` a little below it, such as `--timeout 4m` for a 5-minute cap. It returns one event:

- `timeout` with `reviewOpen: true`: run `sift wait` again. You MUST NOT stop waiting on your own; the human MAY take a while.
- `timeout` with `reviewOpen: false`: nobody is reviewing. Tell the human the review is not open, and run `sift handoff` again or give them `reviewCommand` to run in a terminal. Then wait again.
- `discuss`: the human sent findings back. For each one, read the last `human` message in `thread` and answer with `sift reply <id> --run <run> --text "<answer>"`. Revise the finding with `--title`, `--body`, `--proposal` or `--severity` when their note changes it, or add `--withdraw` when their note shows it is wrong. Answer the question asked; keep replies short. Then wait again.
- `published`: go to step 6. The result already carries the decisions.

A human MAY send the same finding back again after your reply; answer each round the same way.

## 6. Apply the decisions

Edit the original files, not sift's snapshots. Each anchored entry carries `file`, the absolute path to edit. Anchors are snapshot line numbers that shift as your edits land, so you SHOULD locate each change by heading or content. An entry with a `thread` was discussed; what you and the human settled there counts alongside the proposal.

- `apply`: make the change in `proposal` (or address `body` when there is no proposal).
- `applyWithChanges`: make the change as the human's `suggestion` shapes it. Where `suggestion` and `proposal` conflict, `suggestion` wins.
- `rejected`: change nothing. When `reason` is given, you SHOULD NOT reintroduce the idea elsewhere.
- `withdrawn`: nothing to do.
- `note`: the human's closing note on the work as a whole; act on it.

Then tell the human what changed, in a few lines grouped by finding id. If a decision could not be applied as written, say so and why, rather than guessing.
