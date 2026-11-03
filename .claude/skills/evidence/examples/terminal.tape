# A terminal flow, recorded the same way on each side:
#   PATH=<dir of that side's hoop>:$PATH VHS_NO_SANDBOX=true vhs -o main.mp4 terminal.tape </dev/null
# against that side's stack (run-sandbox.sh writes the env file sourced below).
# Each "# Step" line is the explanation the reader sees; the last frame holds the answer.
Set Shell bash
Set FontSize 20
Set Width 1400
Set Height 640
Set Padding 24
Set TypingSpeed 25ms

Hide
Type "source ${HOOP_SANDBOX_DIR:-${TMPDIR:-/tmp}/hoop-sandbox}/env"
Enter
Type `api() { curl -s -H "authorization: Bearer $HOOP_TOKEN" "$HOOP_APIURL$1"; }; clear`
Enter
Show

Type "# Step 1: a user runs a command on the bash connection"
Enter
Sleep 500ms
Type "hoop exec bash -i 'echo hello from the agent'"
Enter
Sleep 4s

Type "# Step 2: the gateway recorded the session, with its exit code"
Enter
Sleep 500ms
Type "api '/api/sessions?limit=1' | jq '.data[0] | {connection, verb, status, exit_code}'"
Enter
Sleep 4s
