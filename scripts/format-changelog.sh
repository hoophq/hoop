#!/bin/bash
set -eo pipefail

# Script to format changelog from git commits into human-readable format
# Usage: format-changelog.sh <input-file> [--ai-summary] [--fetch-prs] [--model MODEL_NAME]

INPUT_FILE="${1}"
USE_AI=""
FETCH_PRS=""
CLAUDE_MODEL=""

# Parse flags
shift
while [[ $# -gt 0 ]]; do
  case "$1" in
    --ai-summary)
      USE_AI="true"
      shift
      ;;
    --fetch-prs)
      FETCH_PRS="true"
      shift
      ;;
    --model)
      CLAUDE_MODEL="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done

if [[ ! -f "$INPUT_FILE" ]]; then
  echo "Error: Input file not found: $INPUT_FILE" >&2
  exit 1
fi

# Temporary files for organizing commits
TEMP_DIR=$(mktemp -d)
trap "rm -rf $TEMP_DIR" EXIT

FEATURES="$TEMP_DIR/features.txt"
FIXES="$TEMP_DIR/fixes.txt"
REFACTORS="$TEMP_DIR/refactors.txt"
CHORES="$TEMP_DIR/chores.txt"
DOCS="$TEMP_DIR/docs.txt"
OTHER="$TEMP_DIR/other.txt"
PR_CACHE="$TEMP_DIR/pr_cache"

# Create PR cache directory
mkdir -p "$PR_CACHE"

# Function to fetch full PR body
fetch_pr_body() {
  local pr_number="$1"
  local cache_file="$PR_CACHE/$pr_number.json"

  # Return cached version if exists
  if [[ -f "$cache_file" ]]; then
    cat "$cache_file"
    return
  fi

  # Fetch full PR data from GitHub
  local pr_data=$(gh pr view "$pr_number" --json title,body,number 2>/dev/null || echo "")

  if [[ -z "$pr_data" ]]; then
    echo "{}"
    return
  fi

  # Cache it
  echo "$pr_data" > "$cache_file"
  echo "$pr_data"
}

# Parse commits and categorize them
parse_commits() {
  local current_commit=""
  local current_type=""
  local pr_number=""

  while IFS= read -r line || [[ -n "$line" ]]; do
    # Check if line starts with a commit hash (7 hex chars followed by space)
    if [[ "$line" =~ ^[0-9a-f]{8}[[:space:]] ]]; then
      # Extract commit message
      commit_msg="${line:9}"  # Skip hash and space

      # Extract PR number if present
      if [[ "$commit_msg" =~ \(#([0-9]+)\) ]]; then
        pr_number="${BASH_REMATCH[1]}"
      else
        pr_number=""
      fi

      # Remove PR number from message for cleaner output
      commit_msg=$(echo "$commit_msg" | sed -E 's/[[:space:]]*\(#[0-9]+\)[[:space:]]*$//')

      # Determine type from commit message prefix
      if [[ "$commit_msg" =~ ^feat:|^feat\(|^[Ff]eature:|^[Aa]dd[[:space:]] ]]; then
        current_type="feature"
        # Clean up the prefix
        commit_msg=$(echo "$commit_msg" | sed -E 's/^(feat|feature)(\([^)]+\))?:[[:space:]]*//' | sed -E 's/^[Aa]dd[[:space:]]//')
      elif [[ "$commit_msg" =~ ^fix:|^fix\(|^[Ff]ixes?:|^[Ff]ixed: ]]; then
        current_type="fix"
        commit_msg=$(echo "$commit_msg" | sed -E 's/^fix(es|ed)?(\([^)]+\))?:[[:space:]]*//')
      elif [[ "$commit_msg" =~ ^refactor:|^refactor\(|^[Rr]efactor[[:space:]] ]]; then
        current_type="refactor"
        commit_msg=$(echo "$commit_msg" | sed -E 's/^refactor(\([^)]+\))?:[[:space:]]*//' | sed -E 's/^[Rr]efactor[[:space:]]//')
      elif [[ "$commit_msg" =~ ^chore:|^chore\( ]]; then
        current_type="chore"
        commit_msg=$(echo "$commit_msg" | sed -E 's/^chore(\([^)]+\))?:[[:space:]]*//')
      elif [[ "$commit_msg" =~ ^docs:|^docs\(|^[Dd]ocumentation:|^[Uu]pdate[[:space:]]README ]]; then
        current_type="docs"
        commit_msg=$(echo "$commit_msg" | sed -E 's/^(docs|documentation)(\([^)]+\))?:[[:space:]]*//')
      elif [[ "$commit_msg" =~ ^rename:|^rename\( ]]; then
        current_type="refactor"
        commit_msg=$(echo "$commit_msg" | sed -E 's/^rename(\([^)]+\))?:[[:space:]]*//')
      elif [[ "$commit_msg" =~ ^enhance:|^enhance\(|^[Ee]nhance[[:space:]]|^[Ii]mprove:|^[Ii]mproved: ]]; then
        current_type="feature"
        commit_msg=$(echo "$commit_msg" | sed -E 's/^(enhance|improve|improved)(\([^)]+\))?:[[:space:]]*//')
      else
        # Try to classify based on keywords if no conventional prefix
        # Look for feature-like keywords: new, mode, UI, architecture
        if [[ "$commit_msg" =~ [Nn]ew[[:space:]][A-Z]|[Mm]ode[[:space:]]-|[Uu]pdated[[:space:]][A-Z]|[Ii]mplemented[[:space:]] ]]; then
          current_type="feature"
        # Look for validation/guardrails (often features)
        elif [[ "$commit_msg" =~ [Vv]alidation|[Gg]uardrail|[Hh]andling ]]; then
          current_type="feature"
        else
          current_type="other"
        fi
      fi

      # Capitalize first letter
      commit_msg="$(tr '[:lower:]' '[:upper:]' <<< ${commit_msg:0:1})${commit_msg:1}"

      # Fetch full PR body if requested
      if [[ -n "$FETCH_PRS" && -n "$pr_number" ]]; then
        fetch_pr_body "$pr_number" > "$TEMP_DIR/pr_${pr_number}.json"
      fi

      # Format with PR number if available
      if [[ -n "$pr_number" ]]; then
        current_commit="- $commit_msg (#$pr_number)"
      else
        current_commit="- $commit_msg"
      fi

      # Write to appropriate file
      case "$current_type" in
        feature) echo "$current_commit" >> "$FEATURES" ;;
        fix) echo "$current_commit" >> "$FIXES" ;;
        refactor) echo "$current_commit" >> "$REFACTORS" ;;
        chore) echo "$current_commit" >> "$CHORES" ;;
        docs) echo "$current_commit" >> "$DOCS" ;;
        other) echo "$current_commit" >> "$OTHER" ;;
      esac
    fi
  done < "$INPUT_FILE"
}

# Function to call Claude API for summarization
summarize_with_ai() {
  local category="$1"
  local items="$2"

  if [[ -z "$ANTHROPIC_API_KEY" ]]; then
    echo "$items"
    return
  fi

  # Load full PR bodies if available
  local pr_context=""
  local pr_numbers=$(echo "$items" | grep -o '#[0-9]\+' | sed 's/#//' || true)

  if [[ -n "$pr_numbers" ]]; then
    for pr_num in $pr_numbers; do
      local pr_file="$TEMP_DIR/pr_${pr_num}.json"
      if [[ -f "$pr_file" ]]; then
        local pr_title=$(jq -r '.title // ""' "$pr_file" 2>/dev/null || echo "")
        local pr_body=$(jq -r '.body // ""' "$pr_file" 2>/dev/null || echo "")

        if [[ -n "$pr_body" ]]; then
          pr_context="$pr_context

---
PR #$pr_num: $pr_title

Full Context:
$pr_body
---"
        fi
      fi
    done
  fi

  # Create a prompt for Claude
  local prompt="You are helping create a customer-facing release changelog. Below are the $category for this release.

Commit Summaries:
$items"

  if [[ -n "$pr_context" ]]; then
    prompt="$prompt

Full Pull Request Details (use this for detailed context):
$pr_context"
  fi

  prompt="$prompt

Please create customer-focused release notes. Read the full PR details above to understand the complete context and value of each change.

Your response should have TWO parts:

1. **Summary Paragraph** (2-3 sentences): Write a brief narrative explaining what's new from a customer perspective. Focus on value and benefits. Synthesize the major themes from the PRs. Only mention features - skip technical implementation details.

2. **Detailed List**: Transform each item into a clear customer benefit. Each bullet should:
   - Start with an action verb (Added, Enabled, Enhanced, Improved, etc.)
   - Be 1 line maximum
   - Explain the value/benefit, not the technical implementation
   - Keep the PR number at the end in the format (#1234)

Use the full PR descriptions to understand WHY each change matters and WHAT value it provides to customers.

Example format:

This release introduces emergency approval workflows and expands monitoring capabilities. Teams can now handle urgent scenarios while maintaining audit compliance, and connect to popular observability tools like Grafana and Kibana.

- Added force approval capability for emergency scenarios with full audit trails (#1228)
- Added Grafana and Kibana as supported connection types with HTTP proxy integration (#1232)
- Enabled file upload support for runbook parameters to simplify automation workflows (#1230)"

  # Call Claude API with multiple fallback models
  # Allow user to specify model, otherwise try multiple models in order
  local models=(
    "${CLAUDE_MODEL}"
    "claude-sonnet-4-5"
    "claude-opus-4-5"
    "claude-sonnet-4-5-20250929"
    "claude-3-5-sonnet-20241022"
    "claude-3-5-sonnet-20240620"
    "claude-3-sonnet-20240229"
  )

  local summary=""
  local model_used=""

  for model in "${models[@]}"; do
    # Skip empty model names
    [[ -z "$model" ]] && continue

    local response=$(curl -s https://api.anthropic.com/v1/messages \
      -H "Content-Type: application/json" \
      -H "x-api-key: $ANTHROPIC_API_KEY" \
      -H "anthropic-version: 2023-06-01" \
      -d @- <<EOF
{
  "model": "$model",
  "max_tokens": 2048,
  "messages": [{
    "role": "user",
    "content": $(jq -Rs . <<< "$prompt")
  }]
}
EOF
)

    # Check if we got a successful response
    summary=$(echo "$response" | jq -r '.content[0].text // empty')

    if [[ -n "$summary" ]]; then
      model_used="$model"
      break
    fi
  done

  if [[ -n "$summary" ]]; then
    # Print model used to stderr for debugging (won't appear in changelog)
    echo "Using model: $model_used" >&2
    echo "$summary"
  else
    # Fallback to original if all API calls fail
    echo "Warning: AI summarization failed, using basic format" >&2
    echo "$items"
  fi
}

# Generate formatted output
generate_output() {
  echo "# Changelog"
  echo ""

  # Features
  if [[ -f "$FEATURES" && -s "$FEATURES" ]]; then
    echo "## 🎉 Features"
    echo ""
    local items=$(cat "$FEATURES")
    if [[ -n "$USE_AI" ]]; then
      summarize_with_ai "new features and capabilities" "$items"
    else
      echo "$items"
    fi
    echo ""
  fi

  # Fixes
  if [[ -f "$FIXES" && -s "$FIXES" ]]; then
    echo "## 🐛 Bug Fixes"
    echo ""
    local items=$(cat "$FIXES")
    echo "$items"
    echo ""
  fi

  # Refactors
  if [[ -f "$REFACTORS" && -s "$REFACTORS" ]]; then
    echo "## ♻️ Refactoring"
    echo ""
    local items=$(cat "$REFACTORS")
    echo "$items"
    echo ""
  fi

  # Documentation
  if [[ -f "$DOCS" && -s "$DOCS" ]]; then
    echo "## 📚 Documentation"
    echo ""
    cat "$DOCS"
    echo ""
  fi

  # Chores
  if [[ -f "$CHORES" && -s "$CHORES" ]]; then
    echo "## 🔧 Maintenance"
    echo ""
    cat "$CHORES"
    echo ""
  fi

  # Other changes
  if [[ -f "$OTHER" && -s "$OTHER" ]]; then
    echo "## 📝 Other Changes"
    echo ""
    cat "$OTHER"
    echo ""
  fi
}

# Main execution
parse_commits
generate_output
