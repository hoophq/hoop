# e2e

Browser tests for the control plane (`hoop start control-plane`, `:8019`)
and a real sidecar (`hoop start sidecar`) connected to it. Each run starts
the control plane on a fresh embedded PGlite database and records video,
trace and screenshots for every test.

- `setup` project: first admin, license intro, saved session.
- `control-plane` project: starts from that session. `sidecar.spec.ts`
  creates a sidecar in the UI, starts the sidecar with the issued token and
  waits until the UI shows it connected.

## CI

`.github/workflows/e2e.yml` runs only on PRs with the `e2e` label. Results
go to Currents when the `CURRENTS_RECORD_KEY` secret is set, and to the
`e2e-report` artifact always.

## Local run

```bash
make build-dev-webapp                          # web UI -> dist/dev/resources/public
go build -o dist/dev/bin/hoop client/hoop.go   # needs GOPRIVATE=github.com/hoophq/libhoop
cd e2e && npm ci && npx playwright install chromium
HOOP_BIN=$PWD/../dist/dev/bin/hoop npm test
npm run report                                 # open the HTML report
```

Port 8019 must be free. Test data is synthetic: traces hold full network
requests, and Currents stores them.
