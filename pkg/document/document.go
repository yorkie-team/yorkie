package document

import (
	"github.com/hackerwins/rottie/pkg/document/change"
	"github.com/hackerwins/rottie/pkg/document/checkpoint"
	"github.com/hackerwins/rottie/pkg/document/json"
	"github.com/hackerwins/rottie/pkg/document/key"
	"github.com/hackerwins/rottie/pkg/document/proxy"
	"github.com/hackerwins/rottie/pkg/document/time"
	"github.com/hackerwins/rottie/pkg/log"
)

type stateType int

const (
	detached stateType = 0
	attached stateType = 1
)

type Document struct {
	key          *key.Key
	state        stateType
	root         *json.Root
	checkpoint   *checkpoint.Checkpoint
	changeID     *change.ID
	localChanges []*change.Change
}

func New(collection, document string) *Document {
	return &Document{
		key:        &key.Key{Collection: collection, Document: document},
		state:      detached,
		root:       json.NewRoot(),
		checkpoint: checkpoint.Initial,
		changeID:   change.InitialID,
	}
}

func (d *Document) Key() *key.Key {
	return d.key
}

func (d *Document) Checkpoint() *checkpoint.Checkpoint {
	return d.checkpoint
}

func (d *Document) Update(
	updater func(root *proxy.ObjectProxy) error,
	message string,
) error {
	ctx := change.NewContext(d.changeID.Next(), message)
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

func (d *Document) HasLocalChanges() bool {
	return len(d.localChanges) > 0
}

func (d *Document) ApplyChangePack(pack *change.Pack) error {
	for _, c := range pack.Changes {
		d.changeID = d.changeID.Sync(c.ID())
		if err := c.Execute(d.root); err != nil {
			return err
		}
	}
	d.checkpoint = d.checkpoint.Forward(pack.Checkpoint)

	return nil
}

func (d *Document) Equals(other *Document) bool {
	return d.Marshal() == other.Marshal()
}

func (d *Document) Marshal() string {
	return d.root.Object().Marshal()
}

func (d *Document) FlushChangePack() *change.Pack {
	changes := d.localChanges
	d.localChanges = []*change.Change{}

	cp := d.checkpoint.IncreaseClientSeq(uint32(len(changes)))
	return change.NewPack(d.key, cp, changes)
}

func (d *Document) SetActor(actor *time.ActorID) {
	for _, c := range d.localChanges {
		c.SetActor(actor)
	}
	d.changeID = d.changeID.SetActor(actor)
}

func (d *Document) Actor() *time.ActorID {
	return d.changeID.Actor()
}
