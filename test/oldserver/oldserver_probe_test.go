//go:build oldserver

// Package oldserver measures what a pinned v0.7.20 server does with a field it
// does not know, against the real binary rather than a simulation.
//
// Run with a v0.7.20 server on OLD_SERVER_ADDR (default localhost:18080):
//
//	go test -tags oldserver ./test/oldserver/ -v
package oldserver

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	gotime "time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/yorkie-team/yorkie/api/converter"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/api/yorkie/v1/v1connect"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/key"
)

func addr() string {
	if a := os.Getenv("OLD_SERVER_ADDR"); a != "" {
		return a
	}
	return "http://localhost:18080"
}

// activate brings up one client against the old server.
func activate(ctx context.Context, t *testing.T, cli v1connect.YorkieServiceClient, name string) time.ActorID {
	res, err := cli.ActivateClient(ctx, connect.NewRequest(&api.ActivateClientRequest{ClientKey: name}))
	assert.NoError(t, err)
	id, err := time.ActorIDFromHex(res.Msg.ClientId)
	assert.NoError(t, err)
	return id
}

// scanOps reports, for every operation in the pack, whether an Add carries
// wire field 5 and what restore_mode an Edit carries.
func scanOps(pack *api.ChangePack) (addUnknown []byte, addSeen int, addKnown string, editMode api.RestoreMode, editSeen int) {
	for _, ch := range pack.Changes {
		for _, op := range ch.Operations {
			switch b := op.Body.(type) {
			case *api.Operation_Add_:
				addSeen++
				if u := b.Add.ProtoReflect().GetUnknown(); len(u) > 0 {
					addUnknown = append(addUnknown, u...)
				}
				// The Add's own known fields. These are the control: if the
				// operation round-tripped at all, these come back.
				addKnown = fmt.Sprintf("parent=%v prev=%v value=%q",
					b.Add.ParentCreatedAt != nil, b.Add.PrevCreatedAt != nil,
					b.Add.Value.GetValue())
			case *api.Operation_Edit_:
				editSeen++
				if b.Edit.RestoreMode != api.RestoreMode_RESTORE_MODE_UNSPECIFIED {
					editMode = b.Edit.RestoreMode
				}
			}
		}
	}
	return
}

// TestOldServerStripsUnknownOperationField pushes one change carrying both a
// field v0.7.20 knows (Edit.restore_mode = 9, the shipped precedent) and a
// field it does not (Add field 5, the number train 1 would take), then reads
// the document back through a second client to see what the server stored.
func TestOldServerStripsUnknownOperationField(t *testing.T) {
	ctx := context.Background()
	cli := v1connect.NewYorkieServiceClient(http.DefaultClient, addr())

	docKey := key.Key(fmt.Sprintf("oldserver-probe-%d", gotime.Now().UnixNano()))

	// 01. Writer attaches with an array and a text.
	writerID := activate(ctx, t, cli, string(docKey)+"-writer")
	doc := document.New(docKey)
	doc.SetActor(writerID)
	assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewArray("arr")
		root.SetNewText("txt").Edit(0, 0, "hello")
		return nil
	}))

	pbPack, err := converter.ToChangePack(doc.CreateChangePack())
	assert.NoError(t, err)
	attachRes, err := cli.AttachDocument(ctx, connect.NewRequest(&api.AttachDocumentRequest{
		ClientId:   writerID.String(),
		ChangePack: pbPack,
	}))
	assert.NoError(t, err)
	docID := attachRes.Msg.DocumentId
	respPack, err := converter.FromChangePack(attachRes.Msg.ChangePack)
	assert.NoError(t, err)
	assert.NoError(t, doc.ApplyChangePack(respPack))

	// 02. One change carrying an Add and an Edit.
	assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetArray("arr").AddString("a")
		root.GetText("txt").Edit(0, 0, "X")
		return nil
	}))
	pbPack2, err := converter.ToChangePack(doc.CreateChangePack())
	assert.NoError(t, err)

	// 03. Stamp both fields on the wire.
	//   - Add: raw field 5 varint 1. These are byte-for-byte the bytes a
	//     regenerated client would emit for `RestoreMode restore_mode = 5`,
	//     so no generator run is needed to produce a faithful new client.
	//
	// Edit.restore_mode is deliberately NOT stamped. v0.7.20 validates it and
	// rejects `restore_mode=RESTORE` with no spans ("invalid restore span"),
	// which would abort the push before the measurement. The control is
	// instead the Add's own known fields, carried on the same message.
	injectedAdds, injectedEdits := 0, 0
	for _, ch := range pbPack2.Changes {
		for _, op := range ch.Operations {
			switch b := op.Body.(type) {
			case *api.Operation_Add_:
				b.Add.ProtoReflect().SetUnknown(protoreflect.RawFields{0x28, 0x01})
				injectedAdds++
			case *api.Operation_Edit_:
				injectedEdits++
			}
		}
	}
	assert.Equal(t, 1, injectedAdds, "expected exactly one Add to stamp")
	assert.Equal(t, 1, injectedEdits, "expected exactly one Edit to stamp")

	// Prove the stamp survives our own marshal before it leaves the process.
	sentUnknown, _, sentKnown, _, _ := scanOps(pbPack2)
	t.Logf("SENT  add-unknown=%x add-known=[%s]", sentUnknown, sentKnown)
	assert.Equal(t, []byte{0x28, 0x01}, sentUnknown, "stamp must be on the outgoing message")

	pushRes, err := cli.PushPullChanges(ctx, connect.NewRequest(&api.PushPullChangesRequest{
		ClientId:   writerID.String(),
		DocumentId: docID,
		ChangePack: pbPack2,
	}))
	if err != nil {
		t.Fatalf("v0.7.20 rejected the push: %v", err)
	}
	t.Logf("PUSH  accepted, server returned %d changes", len(pushRes.Msg.ChangePack.Changes))

	// 04. A second client attaches from scratch and receives what was stored.
	readerID := activate(ctx, t, cli, string(docKey)+"-reader")
	readerDoc := document.New(docKey)
	readerDoc.SetActor(readerID)
	readerPack, err := converter.ToChangePack(readerDoc.CreateChangePack())
	assert.NoError(t, err)
	readRes, err := cli.AttachDocument(ctx, connect.NewRequest(&api.AttachDocumentRequest{
		ClientId:   readerID.String(),
		ChangePack: readerPack,
	}))
	assert.NoError(t, err)

	gotUnknown, addSeen, gotKnown, _, editSeen := scanOps(readRes.Msg.ChangePack)
	t.Logf("READ  changes=%d snapshot=%d adds=%d edits=%d add-unknown=%x add-known=[%s]",
		len(readRes.Msg.ChangePack.Changes), len(readRes.Msg.ChangePack.Snapshot),
		addSeen, editSeen, gotUnknown, gotKnown)

	if addSeen == 0 && editSeen == 0 {
		t.Fatalf("server replied with a snapshot, not operations: the probe cannot observe field survival this way")
	}

	// Positive control: the same Add message's known fields must come back.
	// If they do not, the probe is not observing the operation it stamped and
	// any conclusion about the unknown field would be unattributable.
	assert.Equal(t, 1, addSeen, "CONTROL: exactly one Add should come back")
	assert.Equal(t, sentKnown, gotKnown,
		"CONTROL FAILED: the Add's known fields did not survive — the probe is not measuring what it claims")

	// The measurement.
	if len(gotUnknown) == 0 {
		t.Logf("RESULT old server STRIPPED the unknown field (stored change log lost it)")
	} else {
		t.Logf("RESULT old server PRESERVED the unknown field: %x", gotUnknown)
	}
}

// TestOldServerAcceptsUnknownChangePackField checks the other half of the
// handshake: train 1 wants `ChangePack.capabilities = 9`, and a new client
// will send it to servers that predate it. If v0.7.20 rejected an unknown
// top-level field, the capability negotiation could not be carried there.
//
// It also checks that v0.7.20's *reply* carries no such field, which is what
// makes a default-deny client gate work: absence means "no support".
func TestOldServerAcceptsUnknownChangePackField(t *testing.T) {
	ctx := context.Background()
	cli := v1connect.NewYorkieServiceClient(http.DefaultClient, addr())

	docKey := key.Key(fmt.Sprintf("oldserver-cap-%d", gotime.Now().UnixNano()))
	writerID := activate(ctx, t, cli, string(docKey)+"-writer")

	doc := document.New(docKey)
	doc.SetActor(writerID)
	assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewArray("arr").AddString("a")
		return nil
	}))
	pbPack, err := converter.ToChangePack(doc.CreateChangePack())
	assert.NoError(t, err)

	// `repeated string capabilities = 9` carrying "element-restore":
	// tag (9<<3)|2 = 0x4A, length 15, then the bytes.
	raw := append([]byte{0x4A, 0x0F}, []byte("element-restore")...)
	pbPack.ProtoReflect().SetUnknown(protoreflect.RawFields(raw))
	assert.Equal(t, raw, []byte(pbPack.ProtoReflect().GetUnknown()))

	res, err := cli.AttachDocument(ctx, connect.NewRequest(&api.AttachDocumentRequest{
		ClientId:   writerID.String(),
		ChangePack: pbPack,
	}))
	if err != nil {
		t.Fatalf("RESULT v0.7.20 REJECTED an unknown ChangePack field: %v", err)
	}
	t.Logf("RESULT v0.7.20 accepted an unknown ChangePack field")

	replyUnknown := res.Msg.ChangePack.ProtoReflect().GetUnknown()
	t.Logf("REPLY unknown-fields=%x (empty means a default-deny gate reads 'unsupported')", replyUnknown)
	assert.Empty(t, replyUnknown, "an old server must not appear to advertise a capability")
}
