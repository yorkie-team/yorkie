#!/usr/bin/env bash
set -euo pipefail

# Regenerates docs/tasks/README.md (rich top-level index) and
# docs/tasks/archive/README.md (per-month tables). Leaves
# docs/tasks/active/README.md untouched — that file is hand-written
# prose describing the convention.

TASKS_DIR="${1:-docs/tasks}"
ACTIVE_DIR="$TASKS_DIR/active"
ARCHIVE_DIR="$TASKS_DIR/archive"
TODAY=$(date +%Y-%m-%d)

mkdir -p "$TASKS_DIR" "$ACTIVE_DIR" "$ARCHIVE_DIR"

# Extract first "# Heading" line; fall back to slug.
extract_title() {
  local file="$1"
  local fallback="$2"
  local title
  title=$(grep -m1 '^# ' "$file" 2>/dev/null | sed 's/^# *//' || true)
  if [ -z "$title" ]; then
    title="$fallback"
  fi
  echo "$title"
}

# Extract date for a todo. Prefer the "**Created**: YYYY-MM-DD" line;
# fall back to the YYYYMMDD prefix in the filename.
extract_date() {
  local file="$1"
  local date
  date=$(grep -m1 '^\*\*Created\*\*:' "$file" 2>/dev/null | sed 's/.*: *//' || true)
  if [ -n "$date" ]; then
    echo "$date"
    return
  fi
  local base prefix
  base=$(basename "$file")
  prefix="${base:0:8}"
  if [[ "$prefix" =~ ^[0-9]{8}$ ]]; then
    echo "${prefix:0:4}-${prefix:4:2}-${prefix:6:2}"
  fi
}

# Emit one markdown table row for a todo file.
#   $1: absolute or repo-relative path to *-todo.md
#   $2: link prefix (e.g. "./active/" or "./2026/04/")
render_row() {
  local todo="$1"
  local link_prefix="$2"
  local slug title date lessons lessons_link date_suffix
  slug=$(basename "$todo" -todo.md)
  title=$(extract_title "$todo" "$slug")
  date=$(extract_date "$todo")
  lessons="$(dirname "$todo")/${slug}-lessons.md"
  if [ -f "$lessons" ]; then
    lessons_link="[${slug}-lessons.md](${link_prefix}${slug}-lessons.md)"
  else
    lessons_link="-"
  fi
  date_suffix=""
  if [ -n "$date" ]; then
    date_suffix=" ($date)"
  fi
  echo "| ${title}${date_suffix} | [${slug}-todo.md](${link_prefix}${slug}-todo.md) | ${lessons_link} |"
}

# Count archive *-todo.md files (used for totals).
shopt -s nullglob
archive_files=("$ARCHIVE_DIR"/*/*/*-todo.md)
shopt -u nullglob
archive_count=${#archive_files[@]}

# --- docs/tasks/README.md (top-level rich index) ---
{
  echo "---"
  echo "updated: $TODAY"
  echo "---"
  echo ""
  echo "# Tasks Index"
  echo ""
  echo "Track task-specific plan/review and lessons files using the active/archive layout."
  echo ""
  echo "## Layout"
  echo ""
  echo "- Active tasks: \`docs/tasks/active/\`"
  echo "- Archived tasks: \`docs/tasks/archive/YYYY/MM/\`"
  echo ""
  echo "## Naming"
  echo ""
  echo "- Todo/review: \`YYYYMMDD-<slug>-todo.md\`"
  echo "- Lessons: \`YYYYMMDD-<slug>-lessons.md\`"
  echo ""
  echo "## Active Tasks"
  echo ""
  shopt -s nullglob
  active_files=("$ACTIVE_DIR"/*-todo.md)
  shopt -u nullglob
  if [ ${#active_files[@]} -gt 0 ]; then
    echo "| Task | Todo | Lessons |"
    echo "|---|---|---|"
    # Filenames begin with YYYYMMDD, so reverse lex sort = date descending.
    printf '%s\n' "${active_files[@]}" | sort -r | while IFS= read -r todo; do
      [ -z "$todo" ] && continue
      render_row "$todo" "./active/"
    done
  else
    echo "_(none)_"
  fi
  echo ""
  echo "## Archive"
  echo ""
  echo "- Archived task count: $archive_count"
  echo "- Archive index: [archive/README.md](./archive/README.md)"
} > "$TASKS_DIR/README.md"
echo "Updated $TASKS_DIR/README.md"

# --- docs/tasks/archive/README.md (per-month tables) ---
{
  echo "---"
  echo "updated: $TODAY"
  echo "---"
  echo ""
  echo "# Tasks Archive"
  echo ""
  echo "Completed task records, grouped by year/month."
  echo ""
  echo "- Back to tasks index: [../README.md](../README.md)"
  echo ""
  echo "Total archived tasks: $archive_count"
  echo ""

  shopt -s nullglob
  year_dirs=("$ARCHIVE_DIR"/*/)
  shopt -u nullglob

  if [ ${#year_dirs[@]} -gt 0 ]; then
    sorted_years=$(printf '%s\n' "${year_dirs[@]}" | sort -r)
    while IFS= read -r year_dir; do
      [ -z "$year_dir" ] && continue
      [ -d "$year_dir" ] || continue
      year=$(basename "$year_dir")

      shopt -s nullglob
      month_dirs=("$year_dir"*/)
      shopt -u nullglob
      [ ${#month_dirs[@]} -eq 0 ] && continue

      sorted_months=$(printf '%s\n' "${month_dirs[@]}" | sort -r)
      while IFS= read -r month_dir; do
        [ -z "$month_dir" ] && continue
        [ -d "$month_dir" ] || continue
        month=$(basename "$month_dir")

        shopt -s nullglob
        month_todos=("$month_dir"*-todo.md)
        shopt -u nullglob
        count=${#month_todos[@]}
        [ "$count" -eq 0 ] && continue

        if [ "$count" -eq 1 ]; then
          noun="task"
        else
          noun="tasks"
        fi
        echo "## $year/$month ($count $noun)"
        echo ""
        echo "| Task | Todo | Lessons |"
        echo "|---|---|---|"
        printf '%s\n' "${month_todos[@]}" | sort -r | while IFS= read -r todo; do
          [ -z "$todo" ] && continue
          render_row "$todo" "./$year/$month/"
        done
        echo ""
      done <<< "$sorted_months"
    done <<< "$sorted_years"
  fi
} > "$ARCHIVE_DIR/README.md"
echo "Updated $ARCHIVE_DIR/README.md"

echo "Note: $ACTIVE_DIR/README.md is hand-written and not regenerated."
