/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package document

import (
	"slices"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/attachable"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/resource"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/errors"
	"github.com/yorkie-team/yorkie/pkg/key"
)

type StatusType = attachable.StatusType

const (
	StatusDetached = attachable.StatusDetached
	StatusAttached = attachable.StatusAttached
	StatusRemoved  = attachable.StatusRemoved
)

var (
	// ErrDocumentRemoved occurs when the document is removed.
	ErrDocumentRemoved = errors.FailedPrecond("document is removed")
)

// InternalDocument is a document that is used internally. It is not directly
// exposed to the user.
type InternalDocument struct {
	// key is the key of the document. It is used as the key of the document in
	// user's perspective.
	key key.Key

	// status is the status of the document. It is used to check whether the
	// document is attached to the client or detached or removed.
	status StatusType

	// checkpoint is the checkpoint of the document. It is used to determine
	// what changes should be sent and what changes should be received.
	checkpoint change.Checkpoint

	// changeID is the ID of the last change. It is used to create a new change.
	// It contains logical clock information like the lamport timestamp, actorID
	// and checkpoint information.
	changeID change.ID

	// root is the root of the document. It is used to store JSON-like data in
	// CRDT manner.
	root *crdt.Root

	// presences is the map of the presence. It is used to store the presence
	// of the actors who are attaching this document.
	presences *presence.Map

	// onlineClients is the set of the client who is editing this document in
	// online.
	onlineClients map[string]bool

	// localChanges is the list of the changes that are not yet sent to the
	// server.
	localChanges []*change.Change

	// absorbedRemote records that this document has taken state in from the
	// outside -- a snapshot or a batch of applied changes -- at least once.
	// neverSynced reads it: the checkpoint and the version vector are only
	// indirect evidence, and a snapshot applied to a still-detached document
	// with an initial checkpoint leaves both of them looking untouched while
	// the root holds elements this replica never minted.
	absorbedRemote bool

	// pushed records that a pack built from this document's local changes
	// reached the server, even though nothing came back in: the attach RPC
	// returned and then the client failed before applying the response.
	// neverSynced reads it, because none of the other signals can see that
	// window -- the checkpoint is still initial, the status still Detached and
	// nothing absorbed -- while the server already holds the pushed elements
	// under the actor they were re-issued to. Re-issuing them again, in either
	// direction, would leave the two replicas naming them differently.
	pushed bool

	// mintedActors lists the actors, oldest first, that this never-synced
	// document minted tickets under before its current one. SetActor rewrites
	// the change IDs and each operation's executedAt but leaves the tickets
	// inside an operation -- an element's createdAt, a text node ID, a position
	// -- naming the actor that minted them, so a document the caller renamed
	// through the exported Document.SetActor carries tickets of both. The
	// re-issue has to sweep every one of them onto the attaching actor, or the
	// rebuilt root would hold nodes the replayed positions no longer reach.
	//
	// It is empty for the overwhelmingly common document, which is renamed only
	// by the attach, and reissue clears it: afterwards every ticket names `to`.
	mintedActors []time.ActorID

	// disableGC, when true, declares that this document does not produce or
	// consume tombstones (see docs/design/disable-gc-on-attach.md). It is set
	// by the client on Attach and consumed by ApplyChanges to skip merging
	// remote actors' version vectors into changeID, keeping each subsequent
	// local Change's VV at O(1) for high-fan-out Counter workloads.
	disableGC bool
}

// NewInternalDocument creates a new instance of InternalDocument.
func NewInternalDocument(k key.Key) *InternalDocument {
	root := crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket)

	// TODO(hackerwins): We need to initialize the presence of the actor who edited the document.
	return &InternalDocument{
		key:           k,
		status:        StatusDetached,
		root:          crdt.NewRoot(root),
		checkpoint:    change.InitialCheckpoint,
		changeID:      change.InitialID(),
		presences:     presence.NewMap(),
		onlineClients: make(map[string]bool),
	}
}

// NewInternalDocumentFromSnapshot creates a new instance of InternalDocument with the snapshot.
func NewInternalDocumentFromSnapshot(
	k key.Key,
	serverSeq int64,
	lamport int64,
	vector time.VersionVector,
	snapshot []byte,
) (*InternalDocument, error) {
	obj, presences, err := converter.BytesToSnapshot(snapshot)
	if err != nil {
		return nil, err
	}

	return &InternalDocument{
		key:           k,
		status:        StatusDetached,
		root:          crdt.NewRoot(obj),
		presences:     presences,
		onlineClients: make(map[string]bool),
		checkpoint:    change.InitialCheckpoint.NextServerSeq(serverSeq),
		changeID:      change.InitialID().SetClocks(lamport, vector),

		// The root came from a snapshot, so it holds elements this replica
		// never minted: never re-issue its tickets. See neverSynced.
		absorbedRemote: true,
	}, nil
}

// Key returns the key of this document.
func (d *InternalDocument) Key() key.Key {
	return d.key
}

// Checkpoint returns the checkpoint of this document.
func (d *InternalDocument) Checkpoint() change.Checkpoint {
	return d.checkpoint
}

// SyncCheckpoint syncs the checkpoint and the changeID with the given serverSeq
// and clientSeq.
func (d *InternalDocument) SyncCheckpoint(serverSeq int64, clientSeq uint32) {
	d.changeID = change.NewID(
		clientSeq,
		serverSeq,
		d.changeID.Lamport(),
		d.changeID.ActorID(),
		d.VersionVector(),
	)
	d.checkpoint = d.checkpoint.SyncClientSeq(clientSeq)
}

// HasLocalChanges returns whether this document has local changes or not.
func (d *InternalDocument) HasLocalChanges() bool {
	return len(d.localChanges) > 0
}

// removePushedLocalChanges removes the local changes the server has applied,
// those whose client seq is at most the given one.
func (d *InternalDocument) removePushedLocalChanges(clientSeq uint32) {
	for d.HasLocalChanges() {
		if d.localChanges[0].ClientSeq() > clientSeq {
			break
		}
		d.localChanges = d.localChanges[1:]
	}
}

// SetDisableGC records whether this document participates in GC. The client
// calls this on Attach so subsequent ApplyChanges runs use the lamport-only
// sync path described in docs/design/disable-gc-on-attach.md.
func (d *InternalDocument) SetDisableGC(disableGC bool) {
	d.disableGC = disableGC
}

// ResetPresences clears the presence map and the online-clients set. The
// server calls this when serializing or persisting a snapshot for a
// presenceless document so that no earlier-cached presence entry leaks
// onto the wire or into the snapshots collection.
//
// It takes no lock, so a caller holding a *Document must go through
// Document.ResetPresences. There is no longer a production door from a
// *Document to here: Document.InternalDocumentForTest carries the suffix
// precisely so that reaching the unlocked value is confined to tests.
func (d *InternalDocument) ResetPresences() {
	d.presences = presence.NewMap()
	d.onlineClients = make(map[string]bool)
}

// ApplyChangePack applies the given change pack into this document.
func (d *InternalDocument) ApplyChangePack(pack *change.Pack, disableGC bool) error {
	hasSnapshot := len(pack.Snapshot) > 0

	// 01. Apply remote changes to both the cloneRoot and the document.
	if hasSnapshot {
		if err := d.applySnapshot(pack.Snapshot, pack.VersionVector); err != nil {
			return err
		}
	} else {
		// OpSourceReplay, not OpSourceRemote: this is the server's rebuild
		// path (BuildInternalDocForServerSeq and everything above it), which
		// discards the executed operations returned here. A client applying a
		// remote pack goes through Document.ApplyChangePack instead, which
		// keeps them.
		if _, _, err := d.applyChanges(operations.OpSourceReplay, pack.Changes...); err != nil {
			return err
		}
	}

	// 02. Remove local changes applied to server.
	d.removePushedLocalChanges(pack.Checkpoint.ClientSeq)

	// 03. Update the checkpoint.
	d.checkpoint = d.checkpoint.Forward(pack.Checkpoint)

	if !disableGC && pack.VersionVector != nil && !hasSnapshot {
		if _, err := d.GarbageCollect(pack.VersionVector); err != nil {
			return err
		}
	}

	return nil
}

// GarbageCollect purge elements that were removed before the given time.
func (d *InternalDocument) GarbageCollect(vector time.VersionVector) (int, error) {
	return d.root.GarbageCollect(vector)
}

// GarbageLen returns the count of removed elements.
func (d *InternalDocument) GarbageLen() int {
	return d.root.GarbageLen()
}

// Marshal returns the JSON encoding of this document.
func (d *InternalDocument) Marshal() string {
	return d.root.Object().Marshal()
}

// CreateChangePack creates pack of the local changes to send to the server.
//
// The pack owns its copies of the change slice, of every change's version
// vector and of the pack-level version vector. Document.CreateChangePack
// builds it under d.mu and hands it to the sync goroutine, which serializes
// it with the lock released, while this document keeps appending local
// changes and mutating version vectors in place.
//
// Copying each change matters as much as copying the slice: Context.NextID
// returns the very ID the Change carries, so a buffered local change's
// version vector is the same map as d.changeID's, and ID.SyncClocks and
// ID.SetClocks call VersionVector.Max on it in place while a remote pack is
// applied. Handing the converter that map would let it range over a map
// another goroutine is writing -- an unrecoverable "concurrent map read and
// map write" abort. The copy is shallow apart from the vector: the
// operations are shared, and nothing mutates them after the change is built.
func (d *InternalDocument) CreateChangePack() *change.Pack {
	changes := make([]*change.Change, len(d.localChanges))
	for i, c := range d.localChanges {
		id := c.ID()
		if vector := id.VersionVector(); vector != nil {
			id = id.SetVersionVector(vector.DeepCopy())
		}
		changes[i] = change.New(id, c.Message(), c.Operations(), c.PresenceChange())
	}

	cp := d.checkpoint.IncreaseClientSeq(uint32(len(changes)))
	return change.NewPack(d.key, cp, changes, d.VersionVector().DeepCopy(), nil)
}

// SetActor sets actor into this document. This is also applied in the local
// changes the document has.
//
// It rewrites only the change IDs and each operation's executedAt: the root
// and the tickets an operation carries keep the previous actor. That is what a
// replica built from the server's state needs, where the root holds other
// actors' elements. The client attaching a document it edited offline uses
// ReissueActor instead, which re-issues every ticket the document minted.
//
// On a never-synced document it also moves the version vector's entry onto the
// new actor. change.ID.SetActor deliberately leaves the vector alone -- on a
// synced document the previous actor's entry is a claim other replicas rely on
// -- but a never-synced document's vector holds nothing but its own entry, so
// moving it loses no one else's claim. Without that, a document the caller
// renamed through the exported Document.SetActor would carry a vector keyed on
// the actor it no longer has, and neverSynced -- which reads the vector as
// "names no actor but its own" -- would report false for it forever, skipping
// the pre-attach re-issue the document still needs. The previous actor is
// recorded in mintedActors at the same time, because its tickets are still in
// the root and the operations for the re-issue to sweep.
func (d *InternalDocument) SetActor(actor time.ActorID) {
	prev := d.changeID.ActorID()
	rekey := prev != actor && d.neverSynced()
	if rekey && !slices.Contains(d.mintedActors, prev) {
		d.mintedActors = append(d.mintedActors, prev)
	}

	for i, c := range d.localChanges {
		c.SetActor(actor)
		if !rekey {
			continue
		}

		// Change holds its ID by value, so the vector has to go back through a
		// rebuilt change. The operations are shared, as in reissue.
		id := c.ID()
		d.localChanges[i] = change.New(
			id.SetVersionVector(reissueVersionVector(id.VersionVector(), prev, actor)),
			c.Message(),
			c.Operations(),
			c.PresenceChange(),
		)
	}

	d.changeID = d.changeID.SetActor(actor)
	if rekey {
		d.changeID = d.changeID.SetVersionVector(
			reissueVersionVector(d.changeID.VersionVector(), prev, actor),
		)
	}
}

// ReissueActor sets actor into this document like SetActor and, when the
// document has never synced, re-issues every ticket it minted under its
// previous actor -- usually time.InitialActorID -- to the given actor: in the
// local changes, in the root and in the presences. It reports whether it
// re-issued anything.
//
// Without it, two clients that fill the same key before attaching push values
// with identical createdAt, and the server cannot tell the two elements apart.
// See docs/design/pre-attach-ticket-reissue.md.
//
// The root is rebuilt by replaying the re-issued local changes on a fresh
// root, which is what the server builds from them. The document is left
// untouched when any step fails.
//
// It also returns a rollback that undoes the re-issue, reporting whether it
// ran. The attach that the re-issued tickets are minted for can still fail
// after this returns, and a failed attach must not leave the caller's document
// rewritten; see Document.ReissueActor, which extends the rollback to the
// undo/redo stacks.
//
// A document already carrying the attaching actor still goes through the
// re-issue when it minted tickets under an earlier one: the caller renamed it
// through the exported Document.SetActor to the very actor it then attached
// under, so changeID names `actor` while the root and the operations are full
// of tickets naming the actor before it. Only a document with nothing left in
// mintedActors falls back to SetActor.
func (d *InternalDocument) ReissueActor(actor time.ActorID) (bool, func() bool, error) {
	prev := d.changeID.ActorID()
	hasStaleTickets := prev != actor || len(d.mintedActors) > 0
	if !hasStaleTickets || !d.neverSynced() || !d.HasLocalChanges() {
		d.SetActor(actor)

		// SetActor writes the actor into the very change values the document
		// still holds, so the rollback is the inverse call rather than a
		// restore of the slice. It covers whatever local changes the document
		// holds when it runs, the ones a concurrent Update added included:
		// those were authored under actor too, so they belong in the sweep.
		//
		// It is guarded by neverSynced like the re-issue rollback below: an
		// attach can fail after the server's pack was applied and the
		// attachment registered, and reverting the actor of that live
		// document would stamp its next changes with the wrong actor.
		return false, func() bool {
			if d.changeID.ActorID() != actor || !d.neverSynced() {
				return false
			}
			d.SetActor(prev)
			return true
		}, nil
	}

	if err := d.reissue(prev, actor); err != nil {
		return false, nil, err
	}

	// The rollback runs long after this returns -- after the attach round trip
	// the tickets are minted for -- so it cannot restore a snapshot taken
	// here. The document may have gained local changes in the meantime, from
	// the presence initialization the attach itself makes or from an
	// application goroutine calling Update, and a snapshot restore would drop
	// them on the floor. It re-issues in the other direction instead, which
	// carries those changes back with it.
	//
	// neverSynced is the guard, and the same one the forward re-issue uses: it
	// stops reporting true the moment the document takes the server's attach
	// pack in, which is exactly the state the rollback must not overwrite.
	// Document status is no signal there -- the client puts it back to
	// Detached when it gives an already-applied attach up.
	return true, func() bool {
		if d.changeID.ActorID() != actor || !d.neverSynced() {
			return false
		}
		return d.reissue(actor, prev) == nil
	}, nil
}

// reissue re-issues every ticket this document minted under the actor `from`
// to the actor `to`: in the local changes, in the root and in the presences.
//
// The root is rebuilt by replaying the re-issued local changes on a fresh
// root, which is what the server builds from them. The document is left
// untouched when any step fails: ReissueOperations returns freshly decoded
// operations and the replay runs on a root of its own, so nothing the caller
// holds is written until every step has succeeded.
//
// Only a never-synced document may go through here -- see neverSynced. The
// rebuild reproduces the root from the local changes alone, so a root holding
// anything the document did not mint itself would not survive it.
func (d *InternalDocument) reissue(from, to time.ActorID) error {
	// mintedActors first: the tickets a renamed document still carries under an
	// earlier actor have to move too, or the rebuilt root would hold nodes the
	// replayed positions no longer reach. See the field's comment.
	froms := make([]time.ActorID, 0, len(d.mintedActors)+1)
	for _, actor := range append(slices.Clone(d.mintedActors), from) {
		if actor != to && !slices.Contains(froms, actor) {
			froms = append(froms, actor)
		}
	}

	changes := make([]*change.Change, 0, len(d.localChanges))
	for _, c := range d.localChanges {
		ops := c.Operations()
		id := c.ID().SetActor(to)
		for _, actor := range froms {
			reissuedOps, err := converter.ReissueOperations(ops, actor, to)
			if err != nil {
				return err
			}
			ops = reissuedOps
			id = id.SetVersionVector(reissueVersionVector(id.VersionVector(), actor, to))
		}
		changes = append(changes, change.New(id, c.Message(), ops, c.PresenceChange()))
	}

	root := crdt.NewRoot(crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket))
	presences := presence.NewMap()
	for _, c := range changes {
		if _, err := c.Execute(root, presences, operations.OpSourceReplay); err != nil {
			return err
		}
	}

	d.localChanges = changes
	d.root = root
	d.presences = presences
	changeID := d.changeID.SetActor(to)
	for _, actor := range froms {
		changeID = changeID.SetVersionVector(
			reissueVersionVector(changeID.VersionVector(), actor, to),
		)
	}
	d.changeID = changeID

	// Every ticket names `to` now, so nothing is left for a later sweep.
	d.mintedActors = nil
	return nil
}

// neverSynced reports whether this document has neither sent nor received
// anything: it has absorbed no snapshot and no applied change, its checkpoint
// is the initial one, and its version vector names no actor but its own. Every
// ticket naming its actor was then issued here and is held only by its local
// changes and the state built from them.
//
// "Sent" covers a pack the server stored but whose response this replica never
// took in: the attach RPC returned and the client failed before applying what
// came back. Nothing in the checkpoint, the status or the version vector
// records that, so MarkPushed does, and a document carrying the mark is never
// re-issued again -- neither back to its previous actor by the attach rollback
// nor forward to another one by a later attach.
//
// absorbedRemote is the load-bearing guard, not the checkpoint: applySnapshot
// replaces the root and only its CALLER forwards the checkpoint, so a snapshot
// pack carrying the initial checkpoint would leave the other two signals
// looking untouched while the root is full of elements ReissueActor's rebuild
// cannot reproduce from the local changes.
func (d *InternalDocument) neverSynced() bool {
	if d.pushed || d.absorbedRemote || d.status != StatusDetached || d.checkpoint != change.InitialCheckpoint {
		return false
	}
	actor := d.changeID.ActorID()
	for id := range d.changeID.VersionVector() {
		if id != actor {
			return false
		}
	}
	return true
}

// MarkPushed records that a pack built from this document's local changes
// reached the server. The client calls it when the attach RPC returned and the
// attach then failed, which is the one case the document cannot tell from
// never having synced at all; a successful attach leaves the same evidence
// through the applied response pack.
//
// After it, ReissueActor falls back to SetActor: the elements the server
// stored keep the tickets it stored them under, whichever actor the document
// is attached under next.
func (d *InternalDocument) MarkPushed() {
	d.pushed = true
}

// reissueVersionVector returns a copy of the given vector with the entry of
// the actor `from` moved to the actor `to`. A nil vector is returned as nil:
// DeepCopy would turn it into an empty map, and CreateChangePack tells the two
// apart.
func reissueVersionVector(vector time.VersionVector, from, to time.ActorID) time.VersionVector {
	if vector == nil {
		return nil
	}
	reissued := vector.DeepCopy()
	if from == to {
		return reissued
	}
	if lamport, ok := reissued.Get(from); ok {
		reissued.Unset(from)
		reissued.Set(to, max(lamport, reissued.VersionOf(to)))
	}
	return reissued
}

// Lamport returns the Lamport clock of this document.
func (d *InternalDocument) Lamport() int64 {
	return d.changeID.Lamport()
}

// ActorID returns ID of the actor currently editing the document.
func (d *InternalDocument) ActorID() time.ActorID {
	return d.changeID.ActorID()
}

// VersionVector returns the version vector of this document.
func (d *InternalDocument) VersionVector() time.VersionVector {
	return d.changeID.VersionVector()
}

// SetStatus sets the status of this document.
func (d *InternalDocument) SetStatus(status StatusType) {
	d.status = status
}

// IsAttached returns whether this document is attached or not.
func (d *InternalDocument) IsAttached() bool {
	return d.status == StatusAttached
}

// Root returns the root of this document.
func (d *InternalDocument) Root() *crdt.Root {
	return d.root
}

// DocSize returns the size of the document.
func (d *InternalDocument) DocSize() resource.DocSize {
	return d.root.DocSize()
}

// RootObject returns the root object.
func (d *InternalDocument) RootObject() *crdt.Object {
	return d.root.Object()
}

func (d *InternalDocument) applySnapshot(snapshot []byte, vector time.VersionVector) error {
	rootObj, presences, err := converter.BytesToSnapshot(snapshot)
	if err != nil {
		return err
	}

	d.root = crdt.NewRoot(rootObj)
	d.presences = presences
	d.absorbedRemote = true

	// NOTE(chacha912): Documents created from snapshots were experiencing edit
	// restrictions due to low lamport values.
	// Previously, the code attempted to generate document lamport from ServerSeq.
	// However, after aligning lamport logic with the original research paper,
	// ServerSeq could potentially become smaller than the lamport value.
	// To resolve this, we initialize document's lamport by using the highest
	// lamport value stored in version vector as the starting point.
	d.changeID = d.changeID.SetClocks(vector.MaxLamport(), vector)

	return nil
}

// ApplyChanges applies remote changes to the document. It also returns the
// operations that actually executed, across all changes and in order, so a
// caller with a history layer can reconcile any pending undo/redo entry
// against them.
func (d *InternalDocument) ApplyChanges(changes ...*change.Change) ([]DocEvent, []operations.Operation, error) {
	return d.applyChanges(operations.OpSourceRemote, changes...)
}

// ApplyChangesForReplay applies stored changes to the document exactly like
// ApplyChangePack's non-snapshot branch does: under OpSourceReplay, so the
// reverse-operation and pre-edit-index bookkeeping that only an undo/redo
// history reads is skipped rather than computed and thrown away. Use this,
// not ApplyChanges, for a caller that replays a document's own change log
// into a snapshot-seeded InternalDocument one change at a time outside
// ApplyChangePack itself -- admin.Client.ListChangeSummaries is the one
// production caller today. See OpSourceReplay.
func (d *InternalDocument) ApplyChangesForReplay(
	changes ...*change.Change,
) ([]DocEvent, []operations.Operation, error) {
	return d.applyChanges(operations.OpSourceReplay, changes...)
}

// applyChanges applies the given changes under the given source. Callers that
// keep the executed operations -- Document.applyChanges, whose history layer
// reconciles stacked undo/redo entries against them -- must use
// OpSourceRemote; a caller that replays stored changes and reads neither
// return value -- ApplyChangePack's non-snapshot branch, and
// ApplyChangesForReplay -- uses OpSourceReplay, which skips the
// per-operation bookkeeping only such a history reads. See OpSourceReplay.
func (d *InternalDocument) applyChanges(
	source operations.OpSource,
	changes ...*change.Change,
) ([]DocEvent, []operations.Operation, error) {
	var events []DocEvent
	var executedOps []operations.Operation
	if len(changes) > 0 {
		// Every caller of this feeds it changes that came through a pack --
		// a remote apply, a server replay, or the post-snapshot replay of
		// this document's own pushed changes. None of them is a document
		// that has never synced. See neverSynced.
		d.absorbedRemote = true
	}
	for _, c := range changes {
		var hadPresence, wasOnline bool
		var prevPresence presence.Data
		clientID := c.ID().ActorID().String()

		if c.PresenceChange() != nil {
			hadPresence = d.presences.Has(clientID)
			_, wasOnline = d.onlineClients[clientID]
			prevPresence = d.Presence(clientID)
		}

		result, err := c.Execute(d.root, d.presences, source)
		if err != nil {
			return nil, nil, err
		}
		executedOps = append(executedOps, result.Executed...)

		if c.PresenceChange() != nil {
			if c.PresenceChange().ChangeType == presence.Clear {
				d.RemoveOnlineClient(clientID)
			}
			if event := d.ReconcilePresence(clientID, hadPresence, wasOnline, prevPresence); event != nil {
				events = append(events, *event)
			}
		}

		if d.disableGC {
			d.changeID = d.changeID.SyncLamport(c.ID())
		} else {
			d.changeID = d.changeID.SyncClocks(c.ID())
		}
	}

	return events, executedOps, nil
}

// MyPresence returns the presence of the actor currently editing the document.
func (d *InternalDocument) MyPresence() presence.Data {
	if d.status != StatusAttached {
		return presence.NewData()
	}
	p := d.presences.Load(d.changeID.ActorID().String())
	return p.DeepCopy()
}

// Presence returns the presence of the given client.
// If the client is not online, it returns nil.
func (d *InternalDocument) Presence(clientID string) presence.Data {
	if !d.onlineClients[clientID] {
		return nil
	}

	return d.presences.Load(clientID).DeepCopy()
}

// PresenceForTest returns the presence of the given client
// regardless of whether the client is online or not.
func (d *InternalDocument) PresenceForTest(clientID string) presence.Data {
	return d.presences.Load(clientID).DeepCopy()
}

// Presences returns the presence map of online clients.
func (d *InternalDocument) Presences() map[string]presence.Data {
	presences := make(map[string]presence.Data)
	for clientID := range d.onlineClients {
		p := d.presences.Load(clientID)
		if p == nil {
			continue
		}
		presences[clientID] = p.DeepCopy()
	}
	return presences
}

// AllPresences returns the presence map of all clients
// regardless of whether the client is online or not.
func (d *InternalDocument) AllPresences() map[string]presence.Data {
	return d.presences.ToMap()
}

// SetOnlineClients sets the online clients.
func (d *InternalDocument) SetOnlineClients(ids ...string) {
	d.onlineClients = make(map[string]bool)

	for _, id := range ids {
		d.onlineClients[id] = true
	}
}

// AddOnlineClient adds the given client to the online clients.
func (d *InternalDocument) AddOnlineClient(clientID string) {
	d.onlineClients[clientID] = true
}

// RemoveOnlineClient removes the given client from the online clients.
func (d *InternalDocument) RemoveOnlineClient(clientID string) {
	delete(d.onlineClients, clientID)
}

// ReconcilePresence compares the previous and current state of a client's
// presence/online status and returns the appropriate event to emit.
//
// State transition table:
//
//	(!hadP || !wasOn) → (hasP && isOn)  : WatchedEvent
//	(hadP && wasOn)   → (hasP && isOn)  : PresenceChangedEvent
//	(hadP && wasOn)   → (!hasP || !isOn): UnwatchedEvent
//	otherwise                           : no event (waiting)
func (d *InternalDocument) ReconcilePresence(
	clientID string,
	hadPresence bool,
	wasOnline bool,
	prevPresence presence.Data,
) *DocEvent {
	hasPresence := d.presences.Has(clientID)
	_, isOnline := d.onlineClients[clientID]

	if !hasPresence || !isOnline {
		if hadPresence && wasOnline {
			return &DocEvent{
				Type: UnwatchedEvent,
				Presences: map[string]presence.Data{
					clientID: prevPresence,
				},
			}
		}
		return nil
	}

	if !hadPresence || !wasOnline {
		return &DocEvent{
			Type: WatchedEvent,
			Presences: map[string]presence.Data{
				clientID: d.Presence(clientID),
			},
		}
	}

	return &DocEvent{
		Type: PresenceChangedEvent,
		Presences: map[string]presence.Data{
			clientID: d.Presence(clientID),
		},
	}
}

// ToDocument converts this document to Document.
func (d *InternalDocument) ToDocument() *Document {
	doc := New(d.key)
	doc.setInternalDoc(d)
	return doc
}

// DeepCopy creates a deep copy of this document.
func (d *InternalDocument) DeepCopy() (*InternalDocument, error) {
	root, err := d.root.DeepCopy()
	if err != nil {
		return nil, err
	}

	onlineClients := make(map[string]bool)
	for id := range d.onlineClients {
		onlineClients[id] = true
	}

	return &InternalDocument{
		key:        d.key,
		status:     d.status,
		checkpoint: d.checkpoint,

		// TODO(hackerwins): Previously ChangeID used as an immutable value,
		// but now it is mutable, so we need to create a new instance of ChangeID.
		// COnsider removing this in the future.
		changeID: d.changeID.DeepCopy(),

		root:          root,
		presences:     d.presences.DeepCopy(),
		onlineClients: onlineClients,
		localChanges:  d.localChanges,

		absorbedRemote: d.absorbedRemote,
		pushed:         d.pushed,
		mintedActors:   slices.Clone(d.mintedActors),
	}, nil
}
