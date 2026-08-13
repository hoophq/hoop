#!/bin/bash
# Boots a headless X display showing known PII, then serves it over RDP with
# NLA so the hoop agent will talk to it.
set -euo pipefail

RDP_USER="${RDP_USER:-hooptest}"
# NTLM hash of the password, i.e. MD4(UTF-16LE(password)). WinPR's SAM file is
# how freerdp-shadow-cli authenticates NLA; without it the CredSSP exchange
# fails after the TLS upgrade. Default is the hash of "hooptest".
RDP_NT_HASH="${RDP_NT_HASH:-1bfd292fa08fb78a7772e4e4d201da47}"
SCREEN="${SCREEN:-1280x800x24}"

Xvfb :0 -screen 0 "${SCREEN}" &
for _ in $(seq 1 50); do
    [ -e /tmp/.X11-unix/X0 ] && break
    sleep 0.1
done
export DISPLAY=:0

# Deterministic on-screen PII. Values carry real checksums so a detector that
# validates them (Luhn for the card, the SSN area/group rules) behaves as it
# would in production rather than dismissing them as malformed.
#
# Rendered with a large fixed-width font on purpose: OCR accuracy is not what
# these tests are measuring, and small antialiased text turns a plumbing
# failure into a flaky detection failure.
xterm -geometry 60x20+0+0 -fa Monospace -fs 20 -bg white -fg black \
      -e "bash -c 'cat /dev/stdin <<EOF
CUSTOMER RECORD
  Name   : Jane Doe
  Card   : 4532015112830366
  Expiry : 04/28
  CVV    : 921
  SSN    : 536-90-4399
  Email  : jane.doe@example.com
  Phone  : (415) 555-0142
EOF
sleep infinity'" &

# Let the terminal paint before the first client can connect, so an immediate
# probe sees a stable screen rather than an empty one.
sleep 2

printf '%s:::%s:::\n' "${RDP_USER}" "${RDP_NT_HASH}" > /etc/winpr/SAM
chmod 600 /etc/winpr/SAM

echo "[rdp-testserver] serving ${SCREEN} on :3389 as ${RDP_USER} (NLA)"
exec freerdp-shadow-cli /port:3389 /sec:nla
