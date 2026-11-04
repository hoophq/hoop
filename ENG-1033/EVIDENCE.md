# ENG-1033 · Runbook password inputs are redacted in session labels

PR #2113 · tested on `4ce8ab9` (product code same as `7115511`) against `origin/main` `7a5bfa6` · 2026-11-04

**Result: a runbook `password` input no longer appears in plain text in the session label, on both exec routes; the run still gets the real value.**

## What changes, and why it is risky
Before, anyone who could read a session (UI, Share link, API) saw the password typed into a runbook form. The risk is the run: if the value were redacted before execution, the runbook would get the marker instead of the secret.

## 1. Is the password stored in the session label?
![1-password-in-session-label](https://raw.githubusercontent.com/hoophq/hoop/3bede3a9015a8b200a9a0297b0b54ab9ba178112/ENG-1033/1-password-in-session-label.png)
Look at `secret`: plain text on main, marker on the branch. The legacy route also stored `file` in plain text on main. The script output on the branch still shows `SECRET=S3cr3t-Pa55` (`asenv`).

## 2. A password input referenced twice
![2-password-referenced-twice](https://raw.githubusercontent.com/hoophq/hoop/3bede3a9015a8b200a9a0297b0b54ab9ba178112/ENG-1033/2-password-referenced-twice.png)
On main, the second untyped reference reset the type to `text`: the form showed it unmasked and the label stored it. The branch keeps `password`.

## 3. Re-run sends the label back
![3-rerun-with-redacted-value](https://raw.githubusercontent.com/hoophq/hoop/3bede3a9015a8b200a9a0297b0b54ab9ba178112/ENG-1033/3-rerun-with-redacted-value.png)
On main the run used the marker text as the secret. The branch refuses it with 422 and asks the user to use the form.

## Review env vars
With a review PENDING, the secret appears 0 times (raw or base64) in `GET /sessions/:id`, `GET /reviews` and `GET /reviews/:id` on the branch; once on main, through the label. No API type carries `input_env_vars`; `TestReviewAndSessionTypesDoNotExposeEnvVars` guards it.

## What QA caught on the way
- Repeated input lost its secret type → `TestTemplateParseKeepsSecretTypeOnRepeatedInput`.
- Re-run with redacted values → `TestValidateNotRedactedRefusesLabelMarkers`.
- Guard test matched too few names → widened to any `env`/`secret` field.

## What it solves
Teams that pass secrets through runbooks (for example, into a secret store) no longer expose them to session viewers.

## Open items
- Sessions created before this change keep their labels.
- `private.reviews.input_env_vars` is not cleared (decision: no migration, soft block only).
- Legacy webapp Re-run on such a session now shows the 422 message instead of running.
- CI: all test checks green on 4ce8ab9 (`Sidecar E2E Test` failed on 7115511 for runner disk space, passed on 4ce8ab9). Image publish jobs were still running at write time.
