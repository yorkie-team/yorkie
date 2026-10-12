**Created**: 2026-10-12

# Lessons: tree FindPos element/text boundary

- A main refactor (`FindPos` body moved into `toTreePos`, which returns no
  error) silently broke the merge: git merged cleanly, the compiler did not.
  Build the main merge before pushing to a behind branch.
- Finding the previous visible sibling with one pass over
  `parent.Children()` needs no error path. `FindOffset` added an error that
  every `json.Tree` caller would turn into a panic, for a case that cannot
  happen.
- `leftAnchorID` does not fit here: it anchors a text on its last
  character for restore spans, while this anchor is always an element whose
  own ID is exact.
- A local index→`TreePos` rule is part of the wire contract between SDKs.
  Changing it in one SDK needs a filed JS half, as in the `restore-span`
  task.

## Review rounds

- Round 1 (agent review panel on 69efb81): blocking — no cross-SDK parity
  plan. Fixed by filing yorkie-js-sdk#1481 and recording the rule in
  tree.md. Suggestions addressed: FindOffset error path removed, misleading
  Text comparison in a test comment removed.
