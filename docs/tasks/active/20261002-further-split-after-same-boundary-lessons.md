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

## Review rounds: the GC barrier, and why it came back out

- **`createdAt` does not say where a node has always lived.** A merge moves
  children keeping their original ticket, so "holds a child the editor knew"
  can become true for an empty same-boundary product after the fact. A
  `MergedAt` skip in `holdsKnownChild` was tried and taken back out: the
  field is stamped on a node's *first* merge-move only, `Split`/
  `SplitElement`/`DeepCopy` copy it onto products no merge relocated, and it
  arrives client-supplied with no `findMergeNode` resolution at that call
  site. It is also a replicated ordering rule with no reproducer. Recorded
  as a known limitation in `holdsKnownChild` and in
  `docs/design/concurrent-merge-split.md` instead.
- **A local purge barrier cannot make a tombstone-reading rule GC-safe.**
  Five fix rounds grew a split-chain barrier (ancestors, the tombstone's own
  chain node, `InsNext` successors) and every review found the next hole:
  a chain created after the purge, a successor displaced by a later split,
  `emptyRunReachesActor`'s actor-ID branch. They are one hole. A split
  applied after the purge can carry a tombstone one replica already
  collected into a product nothing named at purge time, so no ticket
  reported then can stand for it. Removing the barrier also removed the
  `GCBarrier` signature change and the per-pass ancestor walk.
- **Ask which way the purge moves the answer.** Unlinking a tombstone only
  takes a known child away, so it can only clear the marker, and a cleared
  marker sends the walk on past the right half — the walk every replica ran
  before this change. That only matters when there is a sibling to walk on
  to, and then the old walk already diverged. So GC can withhold the fix on
  a collecting replica; it cannot break an edit set that converged before.
- **Skipping tombstones the editor saw removed is not the fix either.** It
  makes GC irrelevant — a purge only unlinks a node whose removal every
  later editor knows — but it broke convergence without GC: a three-replica
  split/insert/delete fuzz went from 1164 to 1174 diverging seeds of 3000,
  twelve of them new. A removal the editor knew still marks where the right
  half was.
- **Presence beats a ticket when the ticket cannot be trusted.** The
  `MergedAt` skip failed because it compared a ticket that is stamped on a
  first move only, copied onto products, and client-supplied. Reading
  `MergedFrom` as mere *presence* needs none of that: it is stamped on the
  moved child (not the product), and every way it is wrong clears the marker,
  which drops the walk back to the one `main` runs. A rule whose errors all
  point at the previous behaviour is safe to change without a reproducer; one
  that can invent a new stop point is not.
- **One budget for a walk, not one per step.** `holdsKnownChild` ran a fresh
  subtree descent per chain node. Sharing a single visited set across the
  chain walk closed three findings at once: the per-step amplification, the
  missing cycle guard, and the repeated work — and is sound only because a
  positive answer ends the walk, so the set can only ever cache "nothing
  known here".
- **Measure against `main` before arguing.** A GC-differential fuzz (three
  editing replicas without GC; two observers fed the same change log in
  order, one collecting with the min version vector after each sync)
  showed post-GC divergence on `main` already. Over 4000 split/delete
  scripts the branch without a barrier diverged after GC on the same seeds
  as `main` (15 flat, 13 nested), bar one seed whose minimised script
  diverges on `main` without GC at all. The barrier lowered the count but
  added seeds `main` handled, so it was not a strict improvement either.

## Review round: the merge skip, scoped

- **"Every way it is wrong clears the marker" is not enough when the
  marker never comes back.** Reading `MergedFrom` as bare presence was
  argued safe because a cleared marker falls back to `main`'s walk. It is
  safe per edit, and wrong per document: the field is stamped once and
  never cleared on live content, so one paragraph join anywhere in the
  history blinds §7.8 for good and js#1433 returns. A skip whose errors
  all point at the old behaviour still has to be scoped to the window
  where the disagreement exists — here, a merge the editor had not seen.
  `MergedAt` is usable for exactly that comparison (§7.1 already makes
  it); what the earlier round proved is that it cannot be trusted to
  *add* a stop point, and `nil`/first-move-only both read as "skip".
- **A shared cache must answer the same question it recorded.** The
  descent budget records "nothing known below this subtree". A chain
  node asks "does this node hold the right half", and the two are not
  the same question about the same node. Scanning the entry node afresh
  and caching only the deeper descent keeps the bound and the answer.

## Review round: GC, against the server's ordering

- **Model the system's GC, not "some min vector".** The finding was upheld
  twice on an argument about which way a purge moves the answer. Driving the
  real order instead (push, record the pushed vector, min, pull everything,
  then collect) split the question in two. A removal the editor had not seen
  cannot be collected before the split arrives: the editor's vector covers
  the removal only after the editor has pushed everything it made before.
  A removal the editor had seen can be, and a four-replica script diverged.
  The argument had been right about the first kind and wrong about the
  second.
- **A barrier is sound when the contract says what it is waiting for.** The
  earlier barriers tried to name the product a future split would carry a
  tombstone into, and kept finding the next hole. `PurgeHeldBack` waits on
  something the contract does pin down: every split that has not arrived
  carries a vector at least as large as the min, so a tombstone whose
  ancestors are all inside the min has no unknown ancestor for §7.8 to
  descend from. The hole the old barriers kept hitting (a split made after
  the purge) is still there, and it is narrower than it looked: that split
  and ours both knew the removal, so the editor may itself have collected
  the tombstone.
- **Compare the same question, not the same log.** A first decision fuzz
  flagged diffs that came from the entry gate or from a tree that GC had
  already reshaped earlier. Keying each answer by split, chain sibling and
  live tree isolated `holdsKnownChild`'s own sensitivity: 228 seeds without
  the barrier, 67 with it, 0 for the skip-known-removals rule, which in turn
  diverges without GC on 21-172 more seeds per 20 000.
- **Measure a convergence patch against the version without it.** Both merge
  skips looked like fixes for a real mechanism (a merge in flight moves known
  children). The fuzz says each adds more divergent seeds than it removes.
