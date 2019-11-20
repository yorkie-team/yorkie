package document

import (
	"fmt"

	"github.com/hackerwins/yorkie/pkg/document/change"
	"github.com/hackerwins/yorkie/pkg/document/checkpoint"
	"github.com/hackerwins/yorkie/pkg/document/json"
	"github.com/hackerwins/yorkie/pkg/document/key"
	"github.com/hackerwins/yorkie/pkg/document/proxy"
	"github.com/hackerwins/yorkie/pkg/document/time"
	"github.com/hackerwins/yorkie/pkg/log"
)

type stateType int

const (
	Detached stateType = 0
	Attached stateType = 1
)

// Document represents a document in MongoDB and contains logical clocks.
type Document struct {
	key          *key.Key
	state        stateType
	root         *json.Root
	checkpoint   *checkpoint.Checkpoint
	changeID     *change.ID
	localChanges []*change.Change
}

// New creates a new instance of Document.
func New(collection, document string) *Document {
	return &Document{
		key:        &key.Key{Collection: collection, Document: document},
		state:      Detached,
		root:       json.NewRoot(),
		checkpoint: checkpoint.Initial,
		changeID:   change.InitialID,
	}
}

// Key returns the key of this document.
func (d *Document) Key() *key.Key {
	return d.key
}

// Checkpoint returns the checkpoint of this document.
func (d *Document) Checkpoint() *checkpoint.Checkpoint {
	return d.checkpoint
}

// Update executes the given updater to update this document.
func (d *Document) Update(
	updater func(root *proxy.ObjectProxy) error,
	msgAndArgs ...interface{},
) error {
	ctx := change.NewContext(d.changeID.Next(), messageFromMsgAndArgs(msgAndArgs))
	if err := updater(proxy.ProxyObject(ctx, d.root.Object())); err != nil {
		log.Logger.Error(err)
		return err
	}

	if ctx.HasOperations() {
		c := ctx.ToChange()
		if err := c.Execute(d.root); err != nil {
			return err
		}

		d.localChanges = append(d.localChanges, c)
		d.changeID = ctx.ID()
	}

	return nil
}

// HasLocalChanges returns whether this document has local changes or not.
func (d *Document) HasLocalChanges() bool {
	return len(d.localChanges) > 0
}

// ApplyChangePack applies the given change pack into this document.
func (d *Document) ApplyChangePack(pack *change.Pack) error {
	for _, c := range pack.Changes {
		d.changeID = d.changeID.Sync(c.ID())
		if err := c.Execute(d.root); err != nil {
			return err
		}
	}
	d.checkpoint = d.checkpoint.Forward(pack.Checkpoint)
	log.Logger.Debugf("after apply pack: %s", d.root.Object().Marshal())

	return nil
}

// Marshal returns the JSON encoding of this document.
func (d *Document) Marshal() string {
	return d.root.Object().Marshal()
}

// FlushChangePack flushes the local change pack to send to the remote server.
func (d *Document) FlushChangePack() *change.Pack {
	changes := d.localChanges
	d.localChanges = []*change.Change{}

	cp := d.checkpoint.IncreaseClientSeq(uint32(len(changes)))
	return change.NewPack(d.key, cp, changes)
}

// SetActor sets actor into this document. This is also applied in the local
// changes the document has.
func (d *Document) SetActor(actor *time.ActorID) {
	for _, c := range d.localChanges {
		c.SetActor(actor)
	}
	d.changeID = d.changeID.SetActor(actor)
}

// SetActor sets actor.
func (d *Document) Actor() *time.ActorID {
	return d.changeID.Actor()
}

// UpdateState updates the state of this document.
func (d *Document) UpdateState(state stateType) {
	d.state = state
}

// IsAttached returns the whether this document is attached or not.
func (d *Document) IsAttached() bool {
	return d.state == Attached
}

func messageFromMsgAndArgs(msgAndArgs ...interface{}) string {
	if len(msgAndArgs) == 0 {
		return ""
	}
	if len(msgAndArgs) == 1 {
		msg := msgAndArgs[0]
		if msgAsStr, ok := msg.(string); ok {
			return msgAsStr
		}
		return fmt.Sprintf("%+v", msg)
	}
	if len(msgAndArgs) > 1 {
		return fmt.Sprintf(msgAndArgs[0].(string), msgAndArgs[1:]...)
	}
	return ""
}
