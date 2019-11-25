package client

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"google.golang.org/grpc"

	"github.com/hackerwins/yorkie/api"
	"github.com/hackerwins/yorkie/api/converter"
	"github.com/hackerwins/yorkie/pkg/document"
	"github.com/hackerwins/yorkie/pkg/document/time"
	"github.com/hackerwins/yorkie/pkg/log"
)

type status int

const (
	deactivated status = 0
	activated   status = 1
)

var (
	errClientNotActivated  = errors.New("client is not activated")
	errDocumentNotAttached = errors.New("document is not attached")
)

// Client is a normal client that can communicate with the agent.
// It has documents and sends changes of the document in local
// to the agent to synchronize with other replicas in remote.
type Client struct {
	conn   *grpc.ClientConn
	client api.YorkieClient

	id           *time.ActorID
	key          string
	status       status
	attachedDocs map[string]*document.Document
}

// NewClient creates an instance of Client.
func NewClient(rpcAddr string, opts ...string) (*Client, error) {
	var k string
	if len(opts) == 0 {
		k = uuid.New().String()
	} else {
		k = opts[0]
	}

	conn, err := grpc.Dial(rpcAddr, grpc.WithInsecure())
	if err != nil {
		log.Logger.Error(err)
		return nil, err
	}

	client := api.NewYorkieClient(conn)

	return &Client{
		conn:         conn,
		client:       client,
		key:          k,
		status:       deactivated,
		attachedDocs: make(map[string]*document.Document),
	}, nil
}

// Close closes all resources of this client.
func (c *Client) Close() error {
	if err := c.Deactivate(context.Background()); err != nil {
		return err
	}

	if err := c.conn.Close(); err != nil {
		log.Logger.Error(err)
		return err
	}

	return nil
}

// Activate activates this client. That is, it register itself to the agent
// and receives a unique ID from the agent. The given ID is used to distinguish
// different clients.
func (c *Client) Activate(ctx context.Context) error {
	if c.status == activated {
		return nil
	}

	reply, err := c.client.ActivateClient(ctx, &api.ActivateClientRequest{
		ClientKey: c.key,
	})

	if err != nil {
		log.Logger.Error(err)
		return err
	}

	c.status = activated
	c.id = time.ActorIDFromHex(reply.ClientId)

	return nil
}

// Deactivate deactivates this client.
func (c *Client) Deactivate(ctx context.Context) error {
	if c.status == deactivated {
		return nil
	}

	_, err := c.client.DeactivateClient(ctx, &api.DeactivateClientRequest{
		ClientId: c.id.String(),
	})
	if err != nil {
		log.Logger.Error(err)
		return err
	}

	c.status = deactivated

	return nil
}

// AttachDocument attaches the given document to this client. It tells the agent that
// this client will synchronize the given document.
func (c *Client) AttachDocument(ctx context.Context, doc *document.Document) error {
	if c.status != activated {
		return errClientNotActivated
	}

	doc.SetActor(c.id)

	res, err := c.client.AttachDocument(ctx, &api.AttachDocumentRequest{
		ClientId:   c.id.String(),
		ChangePack: converter.ToChangePack(doc.FlushChangePack()),
	})
	if err != nil {
		log.Logger.Error(err)
		return err
	}

	pack := converter.FromChangePack(res.ChangePack)
	if err := doc.ApplyChangePack(pack); err != nil {
		log.Logger.Error(err)
		return err
	}

	doc.UpdateState(document.Attached)
	c.attachedDocs[doc.Key().BSONKey()] = doc

	return nil
}

// DetachDocument dettaches the given document from this client. It tells the
// agent that this client will no longer synchronize the given document.
//
// To collect garbage things like CRDT tombstones left on the document, all the
// changes should be applied to other replicas before GC time. For this, if the
// document is no longer used by this client, it should be detached.
func (c *Client) DetachDocument(ctx context.Context, doc *document.Document) error {
	if c.status != activated {
		return errClientNotActivated
	}

	if _, ok := c.attachedDocs[doc.Key().BSONKey()]; !ok {
		return errDocumentNotAttached
	}

	res, err := c.client.DetachDocument(ctx, &api.DetachDocumentRequest{
		ClientId:   c.id.String(),
		ChangePack: converter.ToChangePack(doc.FlushChangePack()),
	})
	if err != nil {
		log.Logger.Error(err)
		return err
	}

	pack := converter.FromChangePack(res.ChangePack)
	if err := doc.ApplyChangePack(pack); err != nil {
		log.Logger.Error(err)
		return err
	}

	doc.UpdateState(document.Detached)
	delete(c.attachedDocs, doc.Key().BSONKey())

	return nil
}

// PushPull pushes local changes of the attached documents to the Agent and
// receives changes of the remote replica from the agent then apply them to
// local documents.
func (c *Client) PushPull(ctx context.Context) error {
	if c.status != activated {
		return errClientNotActivated
	}

	for _, doc := range c.attachedDocs {
		res, err := c.client.PushPull(ctx, &api.PushPullRequest{
			ClientId:   c.id.String(),
			ChangePack: converter.ToChangePack(doc.FlushChangePack()),
		})
		if err != nil {
			log.Logger.Error(err)
			return err
		}

		pack := converter.FromChangePack(res.ChangePack)
		if err := doc.ApplyChangePack(pack); err != nil {
			log.Logger.Error(err)
			return err
		}
	}

	return nil
}

// IsActivate returns whether this client is active or not.
func (c *Client) IsActive() bool {
	return c.status == activated
}
