# ENG-854 · Renaming a data masking rule saves the new name

PR #2029 · tested on `82434bf9` against `origin/main` `4ed8c550` · 2026-10-23

**Result: a data masking rule with attributes can now be renamed. On main, the rename fails with a 500 error.**

## What changes, and why it is risky
The rule update now writes `name`. The attribute and sidecar rows follow it through `ON UPDATE CASCADE`. If this went wrong, a renamed rule could lose its attributes and stop masking on the connections that the attributes scope.

## 1. Does the reported rename work?
![1-reported-rename](https://raw.githubusercontent.com/hoophq/hoop/372c4587035dae1299bdaf0f3f7171b134845fb4/ENG-854/1-reported-rename.png)
Main: 500 and the old name stays. Branch: 200, and attribute `teste-ravi` lists the new name.

## 2. What do other renames do?
![2-other-renames](https://raw.githubusercontent.com/hoophq/hoop/372c4587035dae1299bdaf0f3f7171b134845fb4/ENG-854/2-other-renames.png)
Main returns 200 but does not store the name. The branch stores it, and returns 409 for a name that another rule already has.

## What QA caught on the way
- A rename to a used name returned 500. It now returns 409 (`TestUpdateDataMaskingRuleRenameConflict`).
- The OpenAPI doc said the name was immutable. Corrected and regenerated.

## What it solves
Admins can rename data masking rules. On 1.260.0 there is no workaround: the customer must create a new rule and delete the old one.

## Open items
- `CLAAssistant` is red: commit 392e586 has the author `Claude <noreply@anthropic.com>`.
- Not tested: a rename of a rule that is bound to a sidecar (the sandbox has no sidecar).
- The captures come from PGlite sandboxes (`scripts/dev/run-sandbox.sh`), not from Postgres.
