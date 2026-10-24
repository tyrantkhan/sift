# Seeds the round the tapes record: a fresh data root holding one review of docs/tapes/_demo, filed the
# way an agent would. Sourced by the tapes, off camera, so SIFT_HOME and the sift function carry over.
SIFT_ROOT="$(pwd)"
SIFT_HOME="$(mktemp -d)"
export SIFT_HOME
sift() { "$SIFT_ROOT/bin/sift" "$@"; }
cd docs/tapes/_demo || return 1
sift start plan.md store.go --title "Billing service rewrite" --agent Claude >/dev/null
sift add --from findings.json >/dev/null
sift summary --text "I reviewed the rewrite plan and the first billing code for rollout safety, money handling and failure paths. The cutover and the swallowed error matter most." >/dev/null
