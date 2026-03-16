#!/usr/bin/env bash
# Worker post-commit: push the vessel branch.
set -euo pipefail
source /hooks/lib.sh
legion_log INFO "pushing vessel branch"
git push --set-upstream origin "$(git rev-parse --abbrev-ref HEAD)"
