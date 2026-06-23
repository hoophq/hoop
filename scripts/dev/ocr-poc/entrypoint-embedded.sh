#!/usr/bin/env bash
# Supervises the bundled OCR engine (loopback) alongside the agent.
#
# Used by the agent-OCR container images (Dockerfile.agent-ocr[-gpu]). The
# engine listens only on 127.0.0.1 — screen pixels never leave the agent
# process boundary — and the agent reaches it via the baked-in default
# RDP_OCR_SERVER_URL.
#
# Both the engine and the agent are long-lived; this script supervises BOTH
# (it does not exec away). It is fail-loud throughout: if the engine cannot
# start, does not become healthy, or DIES after startup, the container exits
# non-zero rather than running the PII guard blind. tini is PID 1 (set by the
# image ENTRYPOINT) and reaps; this script forwards termination to both
# children and waits for them.
set -euo pipefail

OCR_PORT="${OCR_PORT:-18868}"
OCR_HEALTH_URL="http://127.0.0.1:${OCR_PORT}/healthz"
OCR_START_TIMEOUT="${OCR_START_TIMEOUT:-120}"

OCR_PID=""
AGENT_PID=""
TERMINATING=0

# Forward termination to both children and reap them. Guarded expansions so
# the handler is safe even before a child is assigned. SIGNALS set
# TERMINATING so the supervisor treats the ensuing child exits as a clean
# orchestrated shutdown (exit 0) rather than an engine crash (exit 1).
terminate() {
    TERMINATING=1
    kill "${OCR_PID:-}" "${AGENT_PID:-}" 2>/dev/null || true
}
cleanup() {
    trap - TERM INT HUP QUIT EXIT
    kill "${OCR_PID:-}" "${AGENT_PID:-}" 2>/dev/null || true
    wait "${OCR_PID:-}" "${AGENT_PID:-}" 2>/dev/null || true
}
trap terminate TERM INT HUP QUIT
trap cleanup EXIT

echo "[entrypoint] starting bundled OCR engine on 127.0.0.1:${OCR_PORT}"
# Bind to loopback only: the engine is an in-container detail, never exposed.
python3 -m uvicorn server_rapidocr:app \
    --app-dir /opt/ocr \
    --host 127.0.0.1 \
    --port "${OCR_PORT}" &
OCR_PID=$!

echo "[entrypoint] waiting for OCR engine health (timeout ${OCR_START_TIMEOUT}s)"
deadline=$(( $(date +%s) + OCR_START_TIMEOUT ))
until curl -fsS "${OCR_HEALTH_URL}" >/dev/null 2>&1; do
    if ! kill -0 "${OCR_PID}" 2>/dev/null; then
        echo "[entrypoint] OCR engine exited before becoming healthy" >&2
        exit 1
    fi
    if [ "$(date +%s)" -ge "${deadline}" ]; then
        echo "[entrypoint] OCR engine did not become healthy in time" >&2
        exit 1
    fi
    sleep 1
done
echo "[entrypoint] OCR engine healthy: $(curl -fsS "${OCR_HEALTH_URL}")"

# Start the agent (passed as CMD / args) as a supervised child — NOT exec, so
# this script stays alive to supervise both processes.
"$@" &
AGENT_PID=$!

# Wait for whichever child exits first. If the OCR engine dies after startup,
# the guard would run blind — so we treat either child's exit as terminal and
# propagate a non-zero status when the engine was the one that died.
set +e
wait -n "${OCR_PID}" "${AGENT_PID}"
first_status=$?
set -e

# A signal-initiated shutdown is graceful regardless of which child the
# wait observed exiting first.
if [ "${TERMINATING}" -eq 1 ]; then
    echo "[entrypoint] terminating on signal"
    exit 0
fi

if ! kill -0 "${OCR_PID}" 2>/dev/null; then
    echo "[entrypoint] OCR engine exited while the agent was running — failing the container" >&2
    # cleanup (EXIT trap) terminates the agent; exit non-zero regardless of
    # the engine's own code so the orchestrator restarts the pod.
    exit 1
fi

# Otherwise the agent exited first; propagate its status.
echo "[entrypoint] agent exited with status ${first_status}"
exit "${first_status}"
