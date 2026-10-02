# Lessons: a further split after concurrent same-boundary splits

- **The InsNextID chain is a split lineage, not a boundary.** Every piece
  cut off one element sits in one chain whatever offset it was cut at; a
  walk that means "the same boundary" has to find where that boundary ends.
- **"Holds children" is not "holds the right half".** The first JS version
  stopped at any non-empty sibling; review found that text typed into an
  empty product makes it non-empty too. The marker is a child the editor's
  version vector knows, and it has to be looked for at depth because element
  split products carry fresh tickets.
