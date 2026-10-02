# Lessons: a further split after concurrent same-boundary splits

- **The InsNextID chain is a split lineage, not a boundary.** Every piece
  cut off one element sits in one chain whatever offset it was cut at; a
  walk that means "the same boundary" has to find where that boundary ends.
- **"Holds children" is not "holds the right half".** The first JS version
  stopped at any non-empty sibling; review found that text typed into an
  empty product makes it non-empty too. The marker is a child the editor's
  version vector knows, and it has to be looked for at depth because element
  split products carry fresh tickets.
- **Two walks over the same chain can ask different questions.** A review
  round asked §7.5's empty-run test to use `holdsKnownChild` too, for
  symmetry with §7.8. It diverged a span-end typing raced by an Enter
  (`TestTreeSplitAfterTypingAtSpanEnd`), JS #1435 never made the change, and
  the GC barrier added with it was untested. Both were reverted. A replicated
  rule changes in Go and JS together, with a failing script, or not at all.
