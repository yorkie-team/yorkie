# Lessons: SplitText records the left piece's length in runes

**Created**: 2026-09-24

## What the bug teaches

- `utf16.Decode` returns `[]rune`, so `len()` of its result is a rune count even
  though the slice it came from was indexed in UTF-16 units. The conversion back
  to runes is what silently changes the unit; the two `len()` calls look
  symmetric and are not.
- The split offset is already the length of the left piece. Recomputing a
  quantity you were handed is what let the two disagree.
- Everything downstream — `TreeNodeID.Offset`, `index.Node.VisibleLength`,
  `FindPos` — counts UTF-16 code units because `TreeNode.Length()` does. A unit
  mismatch inside one function stays invisible until a position anchors into the
  affected region, which is why BMP-only text never tripped it.

## Process

- The bug only reaches the server's replay path, not live clients, so the failure
  surfaces as "this document will not load" long after the edit that caused it.
  Tests that assert on a node's recorded length catch it at the source; tests
  that only assert on converged XML do not, since the value is correct — only its
  recorded length is wrong.

## Self review

- Not run. This run is headless and granted no tool that can dispatch a reviewer
  subagent, so no round happened; this is a skipped review, not a clean one. The
  branch's reviewers are CI, `@claude review` and a human.
