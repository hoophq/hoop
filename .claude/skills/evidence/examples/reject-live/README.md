# Live client overlay (ENG-611)

A UI action whose effect lands outside the browser: an approver rejects a
statement in the web app, and a psql held on the sidecar prints the denial.
The recording shows both, so the two sides differ on screen.

1. `python3 -I cors_files.py 18990 <out-dir>/live &`: serves the psql output
   with CORS, so the page can poll it.
2. `record-side.sh main <dsn> <vite-url> <token-file> <gateway-db> 9381 <out-dir>`,
   then the same for `branch`: writes the command line to `live/<side>.out`,
   starts the real psql appending to it, finds the held review and records
   `reject-live.flow.mjs` with `rec.mjs`.
3. `compose-video.sh <out> "BEFORE · origin/main=<out-dir>/main.mp4" "AFTER · branch=<out-dir>/branch.mp4"`.

`reject-live.flow.mjs` injects a fixed "test overlay" panel that polls the
output file every 300 ms, then clicks Reject, types the reason, confirms and
waits for the sidecar's next poll.
