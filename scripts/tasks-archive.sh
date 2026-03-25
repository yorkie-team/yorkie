#!/usr/bin/env bash
set -euo pipefail

TASKS_DIR="${1:-docs/tasks}"
ACTIVE_DIR="$TASKS_DIR/active"
ARCHIVE_DIR="$TASKS_DIR/archive"

if [ ! -d "$ACTIVE_DIR" ]; then
  echo "Error: $ACTIVE_DIR does not exist" >&2
  exit 1
fi

archived=0

for todo in "$ACTIVE_DIR"/*-todo.md; do
  [ -f "$todo" ] || continue

  # 미완료 체크박스가 있으면 건너뛰기
  if grep -q '\- \[ \]' "$todo"; then
    continue
  fi

  # Created 날짜 파싱
  created_line=$(grep -m1 '^\*\*Created\*\*:' "$todo" || true)
  if [ -z "$created_line" ]; then
    echo "Warning: no **Created** line in $(basename "$todo"), skipping" >&2
    continue
  fi

  date_str=$(echo "$created_line" | sed 's/.*: *//')
  year=$(echo "$date_str" | cut -d'-' -f1)
  month=$(echo "$date_str" | cut -d'-' -f2)

  if [ -z "$year" ] || [ -z "$month" ]; then
    echo "Warning: cannot parse date from $(basename "$todo"), skipping" >&2
    continue
  fi

  dest="$ARCHIVE_DIR/$year/$month"
  mkdir -p "$dest"

  # todo 파일 이동
  slug=$(basename "$todo" -todo.md)
  git mv "$todo" "$dest/"
  archived=$((archived + 1))

  # 매칭 lessons 파일이 있으면 함께 이동
  lessons="$ACTIVE_DIR/${slug}-lessons.md"
  if [ -f "$lessons" ]; then
    git mv "$lessons" "$dest/"
  fi

  echo "Archived: $slug → $dest/"
done

if [ "$archived" -eq 0 ]; then
  echo "No completed tasks to archive."
else
  echo "Archived $archived task(s)."
fi
