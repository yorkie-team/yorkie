package admin

import (
	"context"

	"github.com/yorkie-team/yorkie/pkg/types"
	"github.com/yorkie-team/yorkie/yorkie/backend"
	"github.com/yorkie-team/yorkie/yorkie/backend/db"
	"github.com/yorkie-team/yorkie/yorkie/packs"
)

// ListDocumentSummaries returns a list of document summaries.
func ListDocumentSummaries(
	ctx context.Context,
	be *backend.Backend,
	previousID db.ID,
	pageSize int,
) ([]*types.DocumentSummary, error) {
	docInfo, err := be.DB.FindDocInfosByPreviousIDAndPageSize(ctx, previousID, pageSize)
	if err != nil {
		return nil, err
	}

	var summaries []*types.DocumentSummary
	for _, docInfo := range docInfo {
		doc, err := packs.BuildDocumentForServerSeq(ctx, be, docInfo, docInfo.ServerSeq)
		if err != nil {
			return nil, err
		}

		summaries = append(summaries, &types.DocumentSummary{
			ID:       docInfo.ID.String(),
			Key:      doc.Key(),
			Snapshot: doc.Marshal(),
		})
	}

	return summaries, nil
}
