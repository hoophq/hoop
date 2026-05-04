#!/usr/bin/env bash
# Migration Safety Check - Analyzes migration files using Claude API
# Usage: ./scripts/check-migration-safety.sh
#
# Required environment variables:
#   ANTHROPIC_API_KEY - API key for Claude (or proxy auth token)
#
# Optional environment variables:
#   ANTHROPIC_BASE_URL  - Base URL including /v1 path (default: https://api.anthropic.com/v1)
#   ANTHROPIC_AUTH_HEADER - If set, sends an "Authorization" header with this value
#                           (useful for proxied API access through hoop)
#
# Expected input files:
#   /tmp/migration_content.txt - Contents of changed migration files
#   /tmp/changed_files.txt     - List of changed file paths
#
# Output:
#   /tmp/analysis.txt          - The LLM analysis in Markdown
#   Exit code 0 = success, 1 = API failure

set -euo pipefail

# Strip trailing slash from base URL, default to standard Anthropic API
BASE_URL="${ANTHROPIC_BASE_URL:-https://api.anthropic.com/v1}"
BASE_URL="${BASE_URL%/}"

MIGRATION_CONTENT=$(cat /tmp/migration_content.txt)
CHANGED_FILES=$(cat /tmp/changed_files.txt)

SYSTEM_PROMPT='You are a database migration safety analyzer for a Go application that uses golang-migrate (v4) with PostgreSQL.

Context about this project'\''s migration system:
- SQL migrations live in rootfs/app/migrations/ with sequential numbering (000001, 000002, etc.)
- Each migration MUST have both a .up.sql and .down.sql file
- The gateway runs migrations automatically on startup via golang-migrate'\''s m.Up()
- Production builds only ship .up.sql files (down migrations are dev-only)
- The gateway can tolerate running against a DB with a newer schema version (it logs a warning but does not crash)
- If a migration fails mid-execution, the DB enters a "dirty" state requiring manual intervention
- There are also Go-coded migrations that run after SQL migrations -- these are forward-only with no rollback path

Your job is to analyze new or modified migration files and produce a safety report. Focus on:

1. **Rollback Safety**: Can this migration be safely rolled back using the .down.sql? Is the down migration a noop?
2. **Destructive Operations**: Does the .up.sql contain DROP TABLE, DROP COLUMN, TRUNCATE, DELETE, or ALTER TYPE that could lose data?
3. **Down Migration Quality**: Does the .down.sql actually reverse what the .up.sql does? Are there mismatches?
4. **Sandbox Deploy Risk**: If this migration is applied to the sandbox environment, what is the risk level for iterative development? Can we roll back the gateway binary to a previous version without issues?
5. **Missing Pairs**: Are there .up.sql files without corresponding .down.sql files or vice versa?
6. **Best Practices**: Any other concerns (e.g., missing IF EXISTS guards, large table locks, missing indexes on foreign keys, etc.)

Output your analysis as a GitHub-flavored Markdown comment. Use this structure:

## Migration Safety Analysis

### Summary
[One-line overall risk assessment: LOW / MEDIUM / HIGH]

### Files Analyzed
[List of files]

### Findings
[Detailed findings organized by category, using checkboxes:]
- [ ] or [x] for each check

### Recommendations
[Actionable recommendations if any issues found]

Keep it concise but thorough. If everything looks safe, say so clearly.'

USER_PROMPT="Please analyze the following migration files for safety:

Changed files:
${CHANGED_FILES}

File contents:
${MIGRATION_CONTENT}"

# Build curl headers
CURL_HEADERS=(
  -H "content-type: application/json"
  -H "x-api-key: ${ANTHROPIC_API_KEY}"
  -H "anthropic-version: 2023-06-01"
)

# Add Authorization header if provided (for proxied access through hoop)
if [ -n "${ANTHROPIC_AUTH_HEADER:-}" ]; then
  CURL_HEADERS+=(-H "Authorization: ${ANTHROPIC_AUTH_HEADER}")
fi

# Call Claude API using jq to safely build the JSON payload
RESPONSE=$(curl -s --max-time 120 -w "\n%{http_code}" "${BASE_URL}/messages" \
  "${CURL_HEADERS[@]}" \
  -d "$(jq -n \
    --arg system "$SYSTEM_PROMPT" \
    --arg user "$USER_PROMPT" \
    '{
      model: "claude-haiku-4-5",
      max_tokens: 4096,
      system: $system,
      messages: [{role: "user", content: $user}]
    }')")

HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
BODY=$(echo "$RESPONSE" | sed '$d')

if [ "$HTTP_CODE" != "200" ]; then
  echo "Claude API returned HTTP ${HTTP_CODE}"
  echo "$BODY"
  exit 1
fi

# Extract the text content from Claude's response
ANALYSIS=$(echo "$BODY" | jq -r '.content[0].text')

echo "$ANALYSIS" > /tmp/analysis.txt
echo "Migration analysis complete"
