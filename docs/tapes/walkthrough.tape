# The README's hero gif. Redraw it with `make screenshots`.
#
# One round, start to finish: the summary an agent left, a card per finding with the lines it points
# at, one key per decision (an approval, a tweak, a rejection), then a note and the send. docs/tapes/
# seed.sh files the five findings in docs/tapes/demo/findings.json; they are decided in severity
# order, so a change to those findings is a change to the keys below.

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

# Build and seed off camera, so neither appears.
Hide
Type "clear && go build -o bin/sift ./cmd/sift && source docs/tapes/seed.sh && clear" Enter
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
Sleep 3s

# The swallowed error, with the Go it points at: approve with a tweak.
Type "s"
Sleep 800ms
Type "Include the invoice ID in the message too"
Sleep 900ms
Enter
Sleep 3s

# Money as float64: approve.
Type "a"
Sleep 2500ms

# The index: reject it for now.
Type "x"
Sleep 2500ms

# The trivial one: approve. That was the last, so the finish screen opens.
Type "a"
Sleep 2s

# A note on the whole plan, then send. The pane closes and sift says where to go.
Type "Ship the flag first; the rest can follow."
Sleep 1500ms
Enter
Sleep 3500ms
