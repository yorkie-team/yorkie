#!/usr/bin/env bash
# Copyright 2026 The Yorkie Authors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# post-comment.sh — Find an existing PR review comment by HTML marker and
# update it, or create a new review comment if none is found.
#
# Usage:
#   GH_TOKEN=... scripts/ci/post-comment.sh <repo> <pr_number> <marker> <body_file>
#
# Arguments:
#   repo        Repository in "owner/name" format
#   pr_number   Pull request number
#   marker      HTML comment string used as a unique identifier
#   body_file   Path to a markdown file containing the comment body

set -euo pipefail

if [[ $# -ne 4 ]]; then
  echo "Usage: $0 <repo> <pr_number> <marker> <body_file>" >&2
  exit 1
fi

REPO="$1"
PR_NUMBER="$2"
MARKER="$3"
BODY_FILE="$4"

if [[ ! -f "$BODY_FILE" ]]; then
  echo "Error: body_file '$BODY_FILE' not found" >&2
  exit 1
fi

BODY="$(cat "$BODY_FILE")"

# Search existing PR reviews page by page for one whose body starts with the marker.
find_review_id() {
  local page=1
  while true; do
    local reviews
    reviews="$(gh api "repos/${REPO}/pulls/${PR_NUMBER}/reviews?per_page=100&page=${page}")"

    local count
    count="$(echo "$reviews" | jq 'length')"

    if [[ "$count" -eq 0 ]]; then
      echo ""
      return
    fi

    local found_id
    found_id="$(echo "$reviews" | jq --arg marker "$MARKER" \
      '.[] | select(.body | startswith($marker)) | .id' | head -n1)"

    if [[ -n "$found_id" ]]; then
      echo "$found_id"
      return
    fi

    page=$((page + 1))
  done
}

REVIEW_ID="$(find_review_id)"

if [[ -n "$REVIEW_ID" ]]; then
  echo "Updating existing review #${REVIEW_ID} on PR #${PR_NUMBER}..."
  gh api --method PUT "repos/${REPO}/pulls/${PR_NUMBER}/reviews/${REVIEW_ID}" \
    --field body="$BODY" \
    --silent
  echo "Review updated."
else
  echo "Creating new review comment on PR #${PR_NUMBER}..."
  gh api --method POST "repos/${REPO}/pulls/${PR_NUMBER}/reviews" \
    --field body="$BODY" \
    --field event="COMMENT" \
    --silent
  echo "Review created."
fi
