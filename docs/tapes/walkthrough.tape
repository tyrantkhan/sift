# The README's hero gif. Redraw it with `make screenshots`.
#
# One round, start to finish: the summary an agent left, a card per finding with the lines it points
# at, one key per decision (an approval, a tweak, a rejection), a question the agent answers while the
# review stays open, a follow-up and its answer, then a note and the send. docs/tapes/seed.sh files the
# five findings in docs/tapes/_demo/findings.json; they are decided in severity order, so a change to those
# findings is a change to the keys below. docs/tapes/agent.sh plays the agent and answers F3, the money
# finding, so a change to its timing is a change to the sleeps below.

Output docs/assets/walkthrough.gif

Set Shell "bash"
Set Width 1200
Set Height 760
Set FontSize 17
Set Padding 20
Set Framerate 24
Set Theme "Catppuccin Mocha"
Set WindowBar "Colorful"
Set TypingSpeed 45ms

Env TERM "xterm-256color"

# Build, seed and start the agent off camera, so none of it appears. The subshell keeps the agent out of the
# shell's job table, so no job notice lands on the last frame.
Hide
Type `clear && go build -o bin/sift ./cmd/sift && source docs/tapes/seed.sh && (source "$SIFT_ROOT/docs/tapes/agent.sh" >/dev/null 2>&1 &) && clear` Enter
Sleep 6s
Type "sift review" Enter
Sleep 2s
Show

# The summary the agent wrote, and how many findings wait.
Sleep 3s
Enter
Sleep 3s

# Critical first: approve the rollback plan.
Type "a"
Sleep 2500ms

# The swallowed error, with the Go it points at: approve with a tweak.
Type "s"
Sleep 800ms
Type "Include the invoice ID in the message too"
Sleep 900ms
Enter
Sleep 2500ms

# Money as float64: ask the agent first. The review moves on while it thinks.
Type "d"
Sleep 800ms
Type "Does int64 cents work for yen? It has no cents."
Sleep 900ms
Enter
Sleep 2500ms

# The index: reject it for now.
Type "x"
Sleep 2000ms

# The trivial one: approve. The finish screen waits on the agent's answer, which lands about here.
Type "a"
Sleep 5s

# Go to the answer: the revised proposal, with the conversation under it and the code below.
Enter
Sleep 5s

# Follow up in the same thread, and wait for the second answer.
Type "d"
Sleep 800ms
Type "Good. Refuse mixed currencies rather than convert."
Sleep 900ms
Enter
Sleep 5s

# The second answer settles it: approve. Nothing is left, so the send opens.
Enter
Sleep 5s
Type "a"
Sleep 2s

# A note on the whole plan, then send. The pane closes and sift says where to go.
Type "Ship the flag first; the rest can follow."
Sleep 1500ms
Enter
Sleep 3500ms
