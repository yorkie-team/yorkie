package document

import (
	"reflect"

	"github.com/hackerwins/rottie/pkg/document/change"
	"github.com/hackerwins/rottie/pkg/document/checkpoint"
	"github.com/hackerwins/rottie/pkg/document/json"
	"github.com/hackerwins/rottie/pkg/document/proxy"
	"github.com/hackerwins/rottie/pkg/log"
)

type stateType int

const (
	detached stateType = 0
	attached stateType = 1
)

type Key struct {
	collection string
	documentID string
}
type Document struct {
	key          *Key
	state        stateType
	root         *json.Root
	checkpoint   *checkpoint.Checkpoint
	changeID     *change.ID
	localChanges []*change.Change
}

func New(collection, document string) *Document {
	return &Document{
		key:        nil,
		state:      detached,
		root:       json.NewRoot(),
		checkpoint: checkpoint.Initial,
		changeID:   change.InitialID,
	}
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
		if err := d.ApplyChange(c); err != nil {
			return err
		}
		d.changeID = ctx.ID()
	}

	return nil
}

func (d *Document) HasLocalChanges() bool {
	return len(d.localChanges) > 0
}

func (d *Document) ApplyChange(c *change.Change) error {
	if err := c.Execute(d.root); err != nil {
		return err
	}

	d.localChanges = append(d.localChanges, c)
	return nil
}

func (d *Document) Equals(other *Document) bool {
	return reflect.DeepEqual(d, other)
}

func (d *Document) Marshal() string {
	return d.root.Object().Marshal()
}
