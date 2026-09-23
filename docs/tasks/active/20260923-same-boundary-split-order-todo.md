# Concurrent splits of one boundary sit in arrival order

**Created**: 2026-09-23

Tracked as yorkie-team/yorkie-js-sdk#1373.

## Problem

`SplitElement` inserts its product directly after the node it splits. Two
replicas splitting the same node at the same boundary each apply their own
split first and the other second, and the second one lands in front:

```
<doc><p><span>abcde</span></p></doc>, both: EditByPath([0,1], [0,1], nil, 1)

XML, both:  <doc><p><span>abcde</span></p><p></p><p></p></doc>
children:   d1 = [p, split(d2), split(d1)]
            d2 = [p, split(d1), split(d2)]
```

XML and sorted JSON match, so the convergence checks in the hand-crafted and
property-based suites do not see it (the property suite passes 1612 cases
before and after this change). Position-based operations that follow do:

- a range delete over the root, which is what an editor sends when it replaces
  the document, leaves `<p></p>` on one replica for good — its end anchor is
  d1's last child and d2's middle one;
- the empty span left between two halves of a concurrently split span is d2's
  product on d1 and d1's on d2, so each replica shows its own attributes on it.

The server builds snapshots the same way, in its own arrival order, so a
replica that receives a snapshot takes whichever order reached the server.

The JS SDK has had the same placement since element splits existed; #1358
there made `splitByPath` go through `edit`, which is what exposed it to every
paragraph split an editor makes.

## Plan

- [x] Failing test without a server, through protobuf like the wire
      (`pkg/document/tree_split_order_test.go`): paragraph split, span split,
      span + paragraph in one edit and in two, at the end of the text; two
      replicas followed by a range delete, and three replicas each in a
      different arrival order; the empty node's attributes. 11 fail on main.
- [x] `orderSameBoundarySplit` (§7.8): order the products by ticket, newest
      first. A split at the end of its node follows `InsNextID` over unknown
      element split siblings of another actor with a newer ticket and splits
      the last of them at 0.
- [x] §7.5: stop the advance in front of a run of empty unknown split siblings
      that ends at the current actor's own product.
- [x] Design doc §7.8, §7.5 note, Fix 24.
- [x] Mirror in yorkie-js-sdk.

## Review

- `pkg/...` 21 packages, `test/integration` (all), `test/complex -run Tree`
  1612 pass — unchanged from main.
- New test: 11 fail on main, 11 pass.
- End to end with the patched server and the patched JS SDK, a late joiner
  that receives a snapshot agrees with the live replicas in both arrival
  orders, and the range delete that follows converges. Patched client against
  an unpatched server does not: the snapshot flips the receivers.
- Open: content inserted at the very start of a split product concurrently
  with the second split is not covered by the rule (a new sibling paragraph
  is).
