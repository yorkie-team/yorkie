#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT=$(git rev-parse --show-toplevel)

git config core.hooksPath "$REPO_ROOT/.githooks"
echo "Git hooks path set to .githooks/"
