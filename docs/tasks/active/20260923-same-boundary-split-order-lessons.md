# Lessons: concurrent splits of one boundary sit in arrival order

- **Compare trees by node ID, not by XML.** Two empty siblings in swapped
  order print the same. The divergence only surfaced three steps later, in a
  range delete and in attributes on an empty node, which is where an
  application sees it — as two unrelated bugs.
- **Hand changes over through protobuf in serverless tests.** Passing a
  change's pointer to another document lets the receiver rewrite its version
  vector in place; this rule reads the version vector, so the pointer-shared
  test saw every sibling as known and could not exercise the fix at all.
- **A client-only fix makes snapshots worse.** Unpatched, a snapshot happened
  to heal the divergence (the receiver took the server's order); with only the
  clients patched, it causes it. The ordering has to ship in the server and
  both SDKs together.
