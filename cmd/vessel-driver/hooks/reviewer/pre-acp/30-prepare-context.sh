#!/usr/bin/env bash
# Reviewer pre-acp: discover the PR and write review context files for Inquisitor.
set -euo pipefail
source /hooks/lib.sh

legion_require_env LEGION_REVIEW_BRANCH LEGION_CONFIG_JSON

# 1. Discover PR number
PR_NUMBER=$(gh pr list \
  --head "${LEGION_REVIEW_BRANCH}" \
  --base main \
  --state open \
  --json number \
  --jq '.[0].number // empty')

if [ -z "$PR_NUMBER" ]; then
    legion_log ERROR "no open PR found for branch ${LEGION_REVIEW_BRANCH} — was gh pr create successful?"
    exit 1
fi
legion_log INFO "found PR #${PR_NUMBER} for branch ${LEGION_REVIEW_BRANCH}"

# 2. Get original issue ID and rework count from VesselConfig
ORIGINAL_ID=$(echo "$LEGION_CONFIG_JSON" | jq -r '.review_original_issue')
REWORK_COUNT=$(echo "$LEGION_CONFIG_JSON" | jq -r '.review_rework_count // 0')

# 3. Fetch original issue details via bd
ORIGINAL_JSON=$(bd show "$ORIGINAL_ID" --json)
ORIGINAL_TITLE=$(echo "$ORIGINAL_JSON" | jq -r '.[0].title')
ORIGINAL_AC=$(echo "$ORIGINAL_JSON" | jq -r '.[0].acceptance_criteria // .[0].description // "No explicit AC provided"')
ORIGINAL_DESC=$(echo "$ORIGINAL_JSON" | jq -r '.[0].description // ""')

# 4. Compute diff vs main
GIT_DIFF=$(git diff "main...${LEGION_REVIEW_BRANCH}")

# 5. Write review_state.json
mkdir -p /workspace/.legion
printf '{"pr_number":%s,"original_issue_id":"%s","branch":"%s","rework_count":%s}\n' \
    "$PR_NUMBER" "$ORIGINAL_ID" "${LEGION_REVIEW_BRANCH}" "$REWORK_COUNT" \
    > /workspace/.legion/review_state.json
legion_log INFO "wrote /workspace/.legion/review_state.json"

# 6. Write review_context.md for Inquisitor ACP session
cat > /workspace/.legion/review_context.md << REVIEW_EOF
# Review Context

## Original Issue: ${ORIGINAL_TITLE}

### Description
${ORIGINAL_DESC}

### Acceptance Criteria
${ORIGINAL_AC}

## Branch: ${LEGION_REVIEW_BRANCH} (PR #${PR_NUMBER})
### Rework Cycle: ${REWORK_COUNT}

## Diff vs main
\`\`\`diff
${GIT_DIFF}
\`\`\`
REVIEW_EOF
legion_log INFO "wrote /workspace/.legion/review_context.md"