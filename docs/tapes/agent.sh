# Plays the agent in walkthrough.tape: it answers each question the human sends back on F3, the money
# finding, the way an agent does after sift wait. Started in the background after seed.sh, off camera, so it
# shares the round's SIFT_HOME and the sift function. The sleeps let the answers land while the camera watches.
sift wait --timeout 2m >/dev/null || return 1
sleep 6
sift reply F3 \
  --text "Not as written. I'll store minor units with each invoice's currency, so yen keeps whole units." \
  --proposal "Store amounts as \`int64\` minor units with the invoice's currency, and sum integers." >/dev/null || return 1
sift wait --timeout 2m >/dev/null || return 1
sleep 3
sift reply F3 \
  --text "Agreed. Total returns an error when the line items differ in currency." \
  --proposal "Store amounts as \`int64\` minor units with the invoice's currency. Total sums integers and refuses mixed currencies." >/dev/null
