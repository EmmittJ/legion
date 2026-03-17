#!/usr/bin/env bash
# Worker post-clone: configure git identity for commits.
set -euo pipefail
source /hooks/lib.sh
legion_log INFO "configuring git identity"
git config user.email "vessel@legion.local"
git config user.name "Legion Vessel"
