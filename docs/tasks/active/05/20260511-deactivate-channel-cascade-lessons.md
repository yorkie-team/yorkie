**Created**: 2026-05-11

# Lessons: Deactivate channel cascade

## The 30s "deactivate problem" wasn't deactivate's fault

Initial framing was "deactivate doesn't clean up", suggesting a bug in
`clients.Deactivate`. Reading the code showed the async path is sound:
`be.Go` creates a fresh background context, so cancelled request
contexts don't kill the work, and a `ClientDeactivatedEvent` is
produced afterward.

The real story showed up in the server logs: `PublishChannel` fired on
every refresh (attach), but no `Detach` calls or `CHAN: ... expires[]`
cleanup lines appeared until ~60s later. Two separate systems were at
play — client lifecycle (DB `clientInfo`) and channel sessions
(in-memory per-server map with a 60s TTL) — and the cascade between
them simply did not exist. The user's "30s" estimate was actually
~60s + cleanup tick.

Rule: when reported symptoms include a specific number, find the
matching constant in code before forming a hypothesis. The TTL
defaults (`ChannelSessionTTL=60s`, `ChannelSessionCleanupInterval=10s`,
`HousekeepingInterval=30s`) each have a distinct fingerprint in
operator-visible latency.

## SDK fix vs server fix is not really a choice

We considered fixing this in the SDK (send `DetachChannel` on
`pagehide` via `sendBeacon`) instead of cascading from the server.
The SDK path has unfixable limits: every SDK language must implement
it, every browser quirk reappears, and the request still has to leave
the page alive. The server cascade is one place, all SDKs benefit,
and the channel TTL becomes the true last-resort safety net rather
than the primary cleanup mechanism.

Rule: when an "SDK should call X on unload" fix is on the table, ask
whether the server can derive the cleanup itself from an existing
event it already receives. If yes, prefer it — best-effort
cross-network calls during unload are inherently lossy.

## Cluster broadcast was already a paved road

Initial assumption was that adding an actor-keyed cluster RPC would
require building a new "broadcast to all peers" primitive. In fact
`BroadcastChannelList`/`BroadcastChannelCount` already enumerate
cluster nodes via `prepareClusterClients` and fan out per-node RPCs
with partial-failure handling. The new
`BroadcastDetachActorFromChannels` is a near-clone of
`BroadcastChannelCount`.

Rule: before proposing infrastructure, grep for the verb you want
("broadcast", "fanout") in the package. Yorkie has historically added
a broadcast helper per use case rather than a generic one; the
template lives next to the existing helper.

## protoc-gen-go version drift produces fake diffs

`make proto` regenerated `admin.pb.go`, `resources.pb.go`, and
`yorkie.pb.go` with thousands of lines of unrelated changes purely
because the locally-installed `protoc-gen-go` (v1.31.0) didn't match
the version originally used (v1.36.10, recorded in each file's
generated header). The cluster.proto edit was real; the rest was
codegen-style drift that would have inflated the PR diff and confused
reviewers.

Rule: after `make proto`, diff a known-untouched `.pb.go` file. Any
hunks there mean the local toolchain version disagrees with what
generated the committed code — fix the toolchain (install the version
recorded in the file's header comment) and regenerate, rather than
committing the drift.
