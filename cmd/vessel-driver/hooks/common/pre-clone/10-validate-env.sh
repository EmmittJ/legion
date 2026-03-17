#!/usr/bin/env bash
# Common pre-clone: validate required environment before git clone.
# Runs as Tier 1 before the clone so all roles fail fast on missing env.
set -euo pipefail
source /hooks/lib.sh
legion_require_env LEGION_ISSUE_ID LEGION_ROLE GH_TOKEN REPO_URL
