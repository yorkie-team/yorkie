#!/usr/bin/env bash
set -euo pipefail

TASKS_DIR="${1:-docs/tasks}"
ACTIVE_DIR="$TASKS_DIR/active"
ARCHIVE_DIR="$TASKS_DIR/archive"
TODAY=$(date +%Y-%m-%d)

# --- active/README.md ---
{
  echo "---"
  echo "updated: $TODAY"
  echo "---"
  echo ""
  echo "# Active Tasks"
  echo ""
  if ls "$ACTIVE_DIR"/*-todo.md >/dev/null 2>&1; then
    for todo in "$ACTIVE_DIR"/*-todo.md; do
      slug=$(basename "$todo" -todo.md)
      title=$(head -1 "$todo" | sed 's/^# *//')
      echo "- [[$slug-todo]] — $title"
    done
  else
    echo "(없음)"
  fi
} > "$ACTIVE_DIR/README.md"
echo "Updated $ACTIVE_DIR/README.md"

# --- archive/README.md ---
{
  echo "---"
  echo "updated: $TODAY"
  echo "---"
  echo ""
  echo "# Archived Tasks"
  echo ""
  if [ -d "$ARCHIVE_DIR" ]; then
    for year_dir in $(ls -rd "$ARCHIVE_DIR"/*/ 2>/dev/null | grep -v README); do
      year=$(basename "$year_dir")
      for month_dir in $(ls -rd "$year_dir"/*/ 2>/dev/null); do
        month=$(basename "$month_dir")
        echo "## $year-$month"
        echo ""
        for todo in "$month_dir"*-todo.md; do
          [ -f "$todo" ] || continue
          slug=$(basename "$todo" -todo.md)
          title=$(head -1 "$todo" | sed 's/^# *//')
          echo "- [[$slug-todo]] — $title"
        done
        echo ""
      done
    done
  else
    echo "(없음)"
  fi
} > "$ARCHIVE_DIR/README.md"
echo "Updated $ARCHIVE_DIR/README.md"

# --- docs/tasks/README.md ---
{
  echo "---"
  echo "updated: $TODAY"
  echo "---"
  echo ""
  echo "# Tasks"
  echo ""
  echo "- \`active/\` — 진행 중 태스크"
  echo "- \`archive/\` — 완료된 태스크"
} > "$TASKS_DIR/README.md"
echo "Updated $TASKS_DIR/README.md"
