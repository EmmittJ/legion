#!/usr/bin/env bash
# Worker pre-clone: validate required environment before git clone.
set -euo pipefail
source /hooks/lib.sh
legion_require_env LEGION_ISSUE_ID LEGION_ROLE GH_TOKEN