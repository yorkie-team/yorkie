# Identity of Inserted Tree Content

## Problem

Every Tree position anchors by `TreeNodeID`, so an ID must name a single node
([tree](tree.md#one-node-per-treenodeid)). Two nodes under one ID make every
position anchored there ambiguous, which is what left a production document
unopenable.

Undo by copy is the known source. There is a second one, and it needs no undo
at all: an edit that inserts several nodes at once gave them all the same ID.

```go
// pkg/document/json/tree.go, before
ticket := t.context.IssueTimeTicket()
for _, content := range contents {
    node = crdt.NewTreeNode(crdt.NewTreeNodeID(ticket, 0), content.Type, attributes, content.Value)
    for _, child := range content.Children {
        buildDescendants(t.context, child, node)  // one ticket per descendant
    }
}
```

One ticket was issued for the edit and reused for every top-level content,
while descendants each took their own. `EditBulk` with two paragraphs
therefore produced two `<p>` nodes under one ID. The JS SDK issues a ticket per
content and never had this.

It is one of the reasons `dropDuplicateContents` exempts content from the
edit's own change: with a legitimate edit able to mint a collision, the rule
cannot be applied without dropping a node the user just inserted. The other
reason, the simulated split delimiters, is still live — see below.

### Goals

- Every node an edit inserts gets its own identity.
- Documents already stored replay to exactly the state they have now.

### Non-Goals

- The simulated split delimiters, the source this does not remove — see below.
- Undo by copy, the third source — see [undo-redo](undo-redo.md).
- Removing duplicates already stored in documents.

## Design

Each top-level content takes its own ticket. The first keeps the ticket the
edit already issued, so a single-content edit — every edit made through `Edit`
rather than `EditBulk` — assigns exactly the IDs it always did:

```go
nodeTicket := ticket
if i > 0 {
    nodeTicket = t.context.IssueTimeTicket()
}
```

### What this does not fix

Content nodes carry their IDs on the wire, so a replica applies the IDs the
originator chose. Only the tickets an *element split* consumes are recomputed,
by simulating the originator's allocation:

```go
delimiter := e.executedAt.Delimiter()
if contents != nil {
    delimiter += uint32(len(contents))
}
```

That simulation is still wrong, and it is still a live source of duplicate IDs.
It advances by the number of top-level contents while the originator also spends
a ticket per descendant, and the two SDKs do not even agree on what `executedAt`
means: Go publishes it *after* issuing the content tickets, the JS SDK *before*.
Two tree edits in one change reproduce it today:

```go
r.GetTree("t").Edit(2, 2, &json.TreeNode{Type: "text", Value: "q"}, 1)
r.GetTree("t").Edit(1, 1, &json.TreeNode{Type: "text", Value: "z"}, 0)
// → 2:3:...:0 names two nodes
```

Fixing it means either aligning the two conventions or carrying the split
tickets in the operation, both of which change the wire and need a compatibility
window. That is the next piece of work, not this one; see
[tree](tree.md#one-node-per-treenodeid), which lists it as a remaining source.

Because that source is still live, `dropDuplicateContents` keeps exempting
content from the edit's own change. The exemption is load-bearing for current
traffic, not only for replaying old histories: without it, an edit whose split
ticket lands on its own content would have that content dropped.

The same mismatch also separates a Go client's two views of its own document.
`Document.Update` mutates the clone through the json proxies, with the tickets
actually issued, and then executes the change on the root through the
simulation. For an edit with contents and `splitLevel > 0` the two disagree, so
a later edit anchored at a split product can fail against the root while the
clone has already accepted it.

## Risks and Mitigation

| Risk | Mitigation |
|------|------------|
| Newly assigned IDs differ from before for bulk edits | Only for `EditBulk` with more than one content, which is where the IDs collided. Single-content edits are unchanged, including every `Edit` |
| Stored histories replay differently | They do not: content IDs travel on the wire, so replay uses the IDs the change already carries |
| The remaining source, the split simulation, is read as fixed | It is not, and the section above says so with a reproduction. `tree.md` keeps listing it among the sources |

## Alternatives Considered

| Alternative | Why not |
|-------------|---------|
| Carry the split tickets in the operation | The right fix for the simulation, and the next piece of work. It changes the wire on both repos and needs a compatibility window, which is more than this change — removing the bulk-content source — should carry |
| Issue a fresh ticket for every content including the first | Cleaner to read, but it shifts the IDs of every single-content edit — the common case — for no benefit |
| Remove the same-change exemption now | The simulation can still land a split ticket on an edit's own content, so the exemption is what keeps that content from being dropped. It also protects the replay of changes written before this fix |

## See Also

- [tree](tree.md) — the rules that keep an ID naming a single node
- [undo-redo](undo-redo.md) — undo by copy, the other source of duplicate IDs
