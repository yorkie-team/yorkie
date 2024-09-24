package client

import (
	"context"

	"connectrpc.com/connect"

	v1 "github.com/yorkie-team/yorkie/api/yorkie/v1"
)

// ServiceClient TODO.
type ServiceClient interface {
	ActivateClient(
		context.Context,
		*connect.Request[v1.ActivateClientRequest],
	) (*connect.Response[v1.ActivateClientResponse], error)

	DeactivateClient(
		context.Context,
		*connect.Request[v1.DeactivateClientRequest],
	) (*connect.Response[v1.DeactivateClientResponse], error)

	AttachDocument(
		context.Context,
		*connect.Request[v1.AttachDocumentRequest],
	) (*connect.Response[v1.AttachDocumentResponse], error)

	DetachDocument(
		context.Context,
		*connect.Request[v1.DetachDocumentRequest],
	) (*connect.Response[v1.DetachDocumentResponse], error)

	RemoveDocument(
		context.Context,
		*connect.Request[v1.RemoveDocumentRequest],
	) (*connect.Response[v1.RemoveDocumentResponse], error)

	PushPullChanges(
		context.Context,
		*connect.Request[v1.PushPullChangesRequest],
	) (*connect.Response[v1.PushPullChangesResponse], error)

	WatchDocument(
		context.Context,
		*connect.Request[v1.WatchDocumentRequest],
	) (*connect.ServerStreamForClient[v1.WatchDocumentResponse], error)

	Broadcast(
		context.Context,
		*connect.Request[v1.BroadcastRequest],
	) (*connect.Response[v1.BroadcastResponse], error)
}
