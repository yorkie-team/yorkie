# replay-doc

A read-only diagnostic tool that replays a document's persisted state the same
way the server does in `packs.BuildInternalDocForServerSeq`: it loads the
closest snapshot at or before a target `server_seq`, then applies the
subsequent changes **one at a time**. If a change fails to apply (e.g.
`not applicable datatype` from an operation that references a garbage-collected
or wrong-typed CRDT node), the tool reports the exact `server_seq`, actor, and
lamport of the first failing change and the last good `server_seq` — useful for
deciding a rollback point.

It never writes to the database. It uses the raw mongo driver with yorkie's
BSON registry and deliberately avoids `mongo.Dial` (which calls `ensureIndexes`).

## Usage

```sh
# Port-forward mongos (or point -uri at any reachable mongo)
kubectl -n yorkie port-forward svc/mongodb-mongos 27017:27017

go run ./tools/replay-doc \
  -uri "mongodb://admin:admin@localhost:27017/?authSource=admin" \
  -db yorkie-meta \
  -doc <document _id hex> \
  [-to <server_seq>]      # defaults to the document's current server_seq
```

## Output

```
doc key=sheet-... server_seq=604 replay target=604
snapshot server_seq=500 lamport=161 bytes=234832
snapshot loaded ok: elements=4280
replaying 25 changes...

All 25 changes applied cleanly up to server_seq=593. No corruption found in this range.
```

On failure it prints the first offending change and a suggested rollback target:

```
!! APPLY FAILED server_seq=6162 actor=69cd08d8... lamport=... ops=3
   error: not applicable datatype
===> ROLLBACK TARGET: last good server_seq = 6159
```
