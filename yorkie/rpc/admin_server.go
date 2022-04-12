package rpc

import (
	"context"

	"github.com/yorkie-team/yorkie/api"
	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/yorkie/admin"
	"github.com/yorkie-team/yorkie/yorkie/backend"
	"github.com/yorkie-team/yorkie/yorkie/backend/db"
)

type adminServer struct {
	backend *backend.Backend
}

// newAdminServer creates a new adminServer.
func newAdminServer(be *backend.Backend) *adminServer {
	return &adminServer{
		backend: be,
	}
}

// ListDocuments lists documents.
func (s *adminServer) ListDocuments(
	ctx context.Context,
	req *api.ListDocumentsRequest,
) (*api.ListDocumentsResponse, error) {
	documents, err := admin.ListDocumentSummaries(
		ctx,
		s.backend,
		db.ID(req.PreviousId),
		int(req.PageSize),
	)
	if err != nil {
		return nil, err
	}

	return &api.ListDocumentsResponse{
		Documents: converter.ToDocumentSummaries(documents),
	}, nil
}
