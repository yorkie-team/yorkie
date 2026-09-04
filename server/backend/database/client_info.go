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

package database

import (
	"fmt"
	gotime "time"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/errors"
)

// Below are the errors may occur depending on the document and client status.
var (
	ErrClientNotActivated      = errors.FailedPrecond("client not activated").WithCode("ErrClientNotActivated")
	ErrDocumentNotAttached     = errors.FailedPrecond("document not attached").WithCode("ErrDocumentNotAttached")
	ErrDocumentNeverAttached   = errors.FailedPrecond("document never attached").WithCode("ErrDocumentNeverAttached")
	ErrDocumentAlreadyAttached = errors.FailedPrecond("document already attached").WithCode("ErrDocumentAlreadyAttached")
	ErrDocumentAlreadyDetached = errors.FailedPrecond("document already detached").WithCode("ErrDocumentAlreadyDetached")
	ErrAttachedDocumentExists  = errors.FailedPrecond("attached document exists").WithCode("ErrAttachedDocumentExists")
)

// Below are statuses of the client.
const (
	ClientDeactivated = "deactivated"
	ClientActivated   = "activated"
)

// Below are statuses of the document.
const (
	DocumentAttaching = "attaching"
	DocumentAttached  = "attached"
	DocumentDetached  = "detached"
	DocumentRemoved   = "removed"
)

// ClientDocInfo is a structure representing information of the document
// attached to the client.
type ClientDocInfo struct {
	Status    string `bson:"status"`
	ServerSeq int64  `bson:"server_seq"`
	ClientSeq uint32 `bson:"client_seq"`
	Epoch     int64  `bson:"epoch"`
}

// ClientDocInfoMap is a map that associates DocRefKey with ClientDocInfo instances.
type ClientDocInfoMap map[types.ID]*ClientDocInfo

// ClientInfo is a structure representing information of a client.
type ClientInfo struct {
	// ID is the unique ID of the client. It is a fresh per-session ObjectID
	// minted on every activation and used as the session row id for RPC
	// lookups and sharding.
	ID types.ID `bson:"_id"`

	// StableActorID is the stable actor identity derived deterministically from
	// the project ID and client key. Unlike ID, it is the same across activate
	// cycles for the same logical client, so locally-persisted un-pushed changes
	// replay under a consistent actor. It carries no unique index.
	StableActorID types.ID `bson:"stable_actor_id"`

	// ProjectID is the ID of the project the client belongs to.
	ProjectID types.ID `bson:"project_id"`

	// Key is the key of the client. It is used to identify the client by users.
	Key string `bson:"key"`

	// Status is the status of the client.
	Status string `bson:"status"`

	// Documents is a map of document which is attached to the client.
	Documents ClientDocInfoMap `bson:"documents"`

	// Metadata is the metadata of the client.
	Metadata map[string]string `bson:"metadata"`

	// CreatedAt is the time when the client was created.
	CreatedAt gotime.Time `bson:"created_at"`

	// UpdatedAt is the last time the client was accessed.
	// NOTE(hackerwins): The field name is "updated_at" but it is used as
	// "accessed_at".
	UpdatedAt gotime.Time `bson:"updated_at"`
}

// SystemClientInfo returns a ClientInfo instance representing a system client.
// It is used for server-side operations such as restoring documents.
func SystemClientInfo(projectID types.ID, docInfo *DocInfo) *ClientInfo {
	clientInfo := &ClientInfo{
		ID:        types.IDFromActorID(time.InitialActorID),
		ProjectID: projectID,
		Documents: map[types.ID]*ClientDocInfo{
			docInfo.ID: {
				Status:    DocumentAttached,
				ServerSeq: docInfo.ServerSeq,
				ClientSeq: 0,
				Epoch:     docInfo.Epoch,
			},
		},
	}
	return clientInfo
}

// CheckIfInProject checks if the client is in the project.
func (i *ClientInfo) CheckIfInProject(projectID types.ID) error {
	if i.ProjectID != projectID {
		return fmt.Errorf(
			"check client(%s,%s) in project(%s): %w",
			i.ID,
			i.ProjectID,
			projectID,
			ErrClientNotFound,
		)
	}
	return nil
}

// Deactivate sets the status of this client to be deactivated.
func (i *ClientInfo) Deactivate() {
	i.Status = ClientDeactivated
	i.UpdatedAt = gotime.Now()
}

// AttachDocument attaches the given document to this client.
//
// AttachDocument is the sole owner of the seeded checkpoint (server_seq /
// client_seq) on attach; the TryAttaching transition no longer touches those
// seqs (they default to 0 when it creates a fresh Attaching entry). The
// presented checkpoint is the client-supplied pack.Checkpoint and carries the
// resume signal for offline-persistent clients (see
// docs/design/offline-resumable-attach.md, "Conditional checkpoint reset").
// Seeding distinguishes:
//
//   - Case A (same-session re-attach): the client row already holds a
//     ClientDocInfo for docID in Attached/Attaching status with non-zero seqs
//     — server-side row memory is authoritative and is preserved.
//   - Case B (reload / new session): a client presents its persisted checkpoint
//     with a non-zero ServerSeq (the resume signal), so seeding takes it,
//     letting restored un-pushed changes push from the right clientSeq.
//   - Fresh attach: the presented ServerSeq is 0 (never synced with the server),
//     so seed 0/0. A fresh attach that already carries local edits legitimately
//     presents a non-zero ClientSeq with ServerSeq 0; that must still seed 0/0
//     so its pending changes push, hence the resume signal is the ServerSeq, not
//     the ClientSeq.
//
// validateClientSeqContinuity (pushpull.go) remains the loud safety net that
// rejects a clientSeq not continuing the seeded one.
func (i *ClientInfo) AttachDocument(
	docID types.ID,
	alreadyAttached bool,
	epoch int64,
	presented change.Checkpoint,
) error {
	if i.Status != ClientActivated {
		return fmt.Errorf("client(%s) attaches %s: %w",
			i.ID, docID, ErrClientNotActivated)
	}

	if i.Documents == nil {
		i.Documents = make(map[types.ID]*ClientDocInfo)
	}

	if i.IsAlreadyDetached(docID, alreadyAttached) {
		return fmt.Errorf("client(%s) attaches %s: %w",
			i.ID, docID, ErrDocumentAlreadyDetached)
	}

	if i.hasDocument(docID) && i.Documents[docID].Status == DocumentAttached {
		return fmt.Errorf("client(%s) attaches %s: %w",
			i.ID, docID, ErrDocumentAlreadyAttached)
	}

	// Case A: a same-session re-attach whose Attaching/Attached row still
	// carries non-zero seqs is authoritative; preserve them.
	if i.hasDocument(docID) &&
		(i.Documents[docID].ServerSeq != 0 || i.Documents[docID].ClientSeq != 0) &&
		(i.Documents[docID].Status == DocumentAttached ||
			i.Documents[docID].Status == DocumentAttaching) {
		i.Documents[docID].Status = DocumentAttached
		i.Documents[docID].Epoch = epoch
		i.UpdatedAt = gotime.Now()
		return nil
	}

	// Case B vs fresh attach: seed from the presented checkpoint only when it
	// carries the resume signal (a non-zero ServerSeq, i.e. the client has
	// synced with the server before). A fresh attach — even one carrying local
	// edits, whose ClientSeq is non-zero but ServerSeq is 0 — seeds 0/0 so its
	// pending changes are not mistaken for already-pushed work.
	serverSeq := int64(0)
	clientSeq := uint32(0)
	if presented.ServerSeq != 0 {
		serverSeq = presented.ServerSeq
		clientSeq = presented.ClientSeq
	}

	i.Documents[docID] = &ClientDocInfo{
		Status:    DocumentAttached,
		ServerSeq: serverSeq,
		ClientSeq: clientSeq,
		Epoch:     epoch,
	}
	i.UpdatedAt = gotime.Now()

	return nil
}

// IsAlreadyDetached checks if the document is already detached.
func (i *ClientInfo) IsAlreadyDetached(docID types.ID, alreadyAttached bool) bool {
	if alreadyAttached && i.hasDocument(docID) && i.Documents[docID].Status == DocumentDetached {
		return true
	}
	return false
}

// IsAttaching checks if the document is attaching.
func (i *ClientInfo) IsAttaching(docID types.ID) bool {
	if i.hasDocument(docID) && i.Documents[docID].Status == DocumentAttaching {
		return true
	}
	return false
}

// DetachDocument detaches the given document from this client.
func (i *ClientInfo) DetachDocument(docID types.ID) error {
	if err := i.EnsureDocumentAttachedOrAttaching(docID); err != nil {
		return err
	}

	i.Documents[docID].Status = DocumentDetached
	i.Documents[docID].ClientSeq = 0
	i.Documents[docID].ServerSeq = 0
	i.UpdatedAt = gotime.Now()

	return nil
}

// RemoveDocument removes the given document from this client.
func (i *ClientInfo) RemoveDocument(docID types.ID) error {
	if err := i.EnsureDocumentAttachedOrAttaching(docID); err != nil {
		return err
	}

	i.Documents[docID].Status = DocumentRemoved
	i.Documents[docID].ClientSeq = 0
	i.Documents[docID].ServerSeq = 0
	i.UpdatedAt = gotime.Now()

	return nil
}

// IsAttached returns whether the given document is attached to this client.
func (i *ClientInfo) IsAttached(docID types.ID) (bool, error) {
	if !i.hasDocument(docID) {
		return false, fmt.Errorf("check %s is attached: %w",
			docID, ErrDocumentNeverAttached)
	}

	return i.Documents[docID].Status == DocumentAttached, nil
}

// Checkpoint returns the checkpoint of the given document.
func (i *ClientInfo) Checkpoint(docID types.ID) change.Checkpoint {
	clientDocInfo := i.Documents[docID]
	if clientDocInfo == nil {
		return change.InitialCheckpoint
	}

	return change.NewCheckpoint(clientDocInfo.ServerSeq, clientDocInfo.ClientSeq)
}

// UpdateCheckpoint updates the checkpoint of the given document.
func (i *ClientInfo) UpdateCheckpoint(
	docID types.ID,
	cp change.Checkpoint,
) error {
	if !i.hasDocument(docID) {
		return fmt.Errorf("update checkpoint in %s: %w", docID, ErrDocumentNeverAttached)
	}

	i.Documents[docID].ServerSeq = cp.ServerSeq
	i.Documents[docID].ClientSeq = cp.ClientSeq
	i.UpdatedAt = gotime.Now()

	return nil
}

// ServerSeq returns the server sequence of the given document.
func (i *ClientInfo) ServerSeq(
	docID types.ID,
) (int64, error) {
	if !i.hasDocument(docID) {
		return 0, fmt.Errorf("document not found %s: %w", docID, ErrDocumentNotFound)
	}

	return i.Documents[docID].ServerSeq, nil
}

// EnsureActivated ensures the client is activated.
func (i *ClientInfo) EnsureActivated() error {
	if i.Status != ClientActivated {
		return fmt.Errorf("ensure activated client(%s): %w", i.ID, ErrClientNotActivated)
	}

	return nil
}

// EnsureDocumentAttached ensures the given document is attached.
func (i *ClientInfo) EnsureDocumentAttached(docID types.ID) error {
	if i.Status != ClientActivated {
		return fmt.Errorf("ensure attached %s in client(%s): %w",
			docID, i.ID, ErrClientNotActivated)
	}

	if !i.hasDocument(docID) || i.Documents[docID].Status != DocumentAttached {
		return fmt.Errorf("ensure attached %s in client(%s): %w",
			docID, i.ID, ErrDocumentNotAttached)
	}

	return nil
}

// EnsureDocumentAttachedOrAttaching ensures the given document is attached or attaching.
func (i *ClientInfo) EnsureDocumentAttachedOrAttaching(docID types.ID) error {
	if i.Status != ClientActivated {
		return fmt.Errorf("ensure attached or attaching %s in client(%s): %w",
			docID, i.ID, ErrClientNotActivated)
	}

	if !i.hasDocument(docID) ||
		(i.Documents[docID].Status != DocumentAttached &&
			i.Documents[docID].Status != DocumentAttaching) {
		return fmt.Errorf("ensure attached or attaching %s in client(%s): %w",
			docID, i.ID, ErrDocumentNotAttached)
	}

	return nil
}

// EnsureDocumentsNotAttachedWhenDeactivated ensures that no documents are attached
// when the client is deactivated.
func (i *ClientInfo) EnsureDocumentsNotAttachedWhenDeactivated() error {
	if i.Status != ClientDeactivated {
		return nil
	}

	for docID := range i.Documents {
		isAttached, err := i.IsAttached(docID)
		if err != nil {
			return err
		}

		if isAttached {
			return ErrAttachedDocumentExists
		}
	}

	return nil
}

// DeepCopy returns a deep copy of this client info.
func (i *ClientInfo) DeepCopy() *ClientInfo {
	if i == nil {
		return nil
	}

	documents := make(map[types.ID]*ClientDocInfo, len(i.Documents))
	for docID, docInfo := range i.Documents {
		documents[docID] = &ClientDocInfo{
			Status:    docInfo.Status,
			ServerSeq: docInfo.ServerSeq,
			ClientSeq: docInfo.ClientSeq,
			Epoch:     docInfo.Epoch,
		}
	}

	return &ClientInfo{
		ID:            i.ID,
		StableActorID: i.StableActorID,
		ProjectID:     i.ProjectID,
		Key:           i.Key,
		Status:        i.Status,
		Documents:     documents,
		Metadata:      i.Metadata,
		CreatedAt:     i.CreatedAt,
		UpdatedAt:     i.UpdatedAt,
	}
}

func (i *ClientInfo) hasDocument(docID types.ID) bool {
	return i.Documents != nil && i.Documents[docID] != nil
}

// RefKey returns the refKey of the client.
func (i *ClientInfo) RefKey() types.ClientRefKey {
	return types.ClientRefKey{
		ProjectID: i.ProjectID,
		ClientID:  i.ID,
	}
}

// IsOwnActor reports whether the given actorID belongs to this client, i.e.
// whether a change or a version-vector entry stamped with actorID is this
// client's own. It compares against both identities the client may stamp: the
// per-session ID (old SDKs) and the StableActorID (new SDKs). Rows written
// before StableActorID existed leave it empty, so an empty StableActorID never
// matches.
//
// This compare-both check is the backward-compatible key correctness switch for
// self-echo dedup, version-vector liveness, min-VV, and GC. The hard invariant:
// the actor stamped into a change must be recognizable as this client's own by
// the same predicate that keys dedup/VV/GC, or GC can advance past un-synced
// tombstones.
func (i *ClientInfo) IsOwnActor(actorID types.ID) bool {
	return i.ID == actorID || (i.StableActorID != "" && i.StableActorID == actorID)
}

// OwnActorID returns the actor this client stamps into its own changes: the
// StableActorID for new SDKs, or the per-session ID for old SDKs (and for rows
// written before StableActorID existed). Use it when a value must be keyed by
// the client's own actor, e.g. the size-1 version vector returned to a
// GC-disabled client so its lamport clock advances under the right key.
func (i *ClientInfo) OwnActorID() (time.ActorID, error) {
	if i.StableActorID != "" {
		return i.StableActorID.ToActorID()
	}
	return i.ID.ToActorID()
}

// IsServerClient returns true if this client represents a server‐side process.
func (i *ClientInfo) IsServerClient() bool {
	actorID, err := i.ID.ToActorID()
	if err != nil {
		return false
	}

	return actorID == time.InitialActorID
}

// UpdateDocStatus updates the status of the document in the client info.
func (i *ClientInfo) UpdateDocStatus(
	docID types.ID,
	status document.StatusType,
	cp change.Checkpoint,
) error {
	switch status {
	case document.StatusRemoved:
		return i.RemoveDocument(docID)
	case document.StatusDetached:
		return i.DetachDocument(docID)
	default:
		return i.UpdateCheckpoint(docID, cp)
	}
}

// AttachedDocuments returns the list of document IDs attached to this client.
func (i *ClientInfo) AttachedDocuments() []types.ID {
	if i.Documents == nil {
		return nil
	}

	docIDs := make([]types.ID, 0, len(i.Documents))
	for docID := range i.Documents {
		if i.Documents[docID].Status != DocumentAttached {
			continue
		}

		docIDs = append(docIDs, docID)
	}

	return docIDs
}
