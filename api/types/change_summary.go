package types

import "github.com/yorkie-team/yorkie/pkg/document/change"

// ChangeSummary represents a summary of change.
type ChangeSummary struct {
	// ID is the unique identifier of the change.
	ID change.ID

	// Message is the message of the change.
	Message string

	// Snapshot is the snapshot of the document.
	Snapshot string
}

// GetChangesRange returns a range of changes.
func GetChangesRange(
	paging Paging[int64],
	lastSeq int64,
) (int64, int64) {
	if paging.Offset == 0 && paging.PageSize == 0 {
		return 1, lastSeq
	}

	size := int64(paging.PageSize)
	prevSeq := paging.Offset
	var from, to int64
	if paging.IsForward {
		if prevSeq == 0 {
			from = 1
			to = size
			if size > lastSeq {
				to = lastSeq
			}
		} else if prevSeq >= lastSeq {
			from = lastSeq + 1
			to = lastSeq + 1
		} else {
			from = prevSeq + 1
			to = prevSeq + size
			if size == 0 || to > lastSeq {
				to = lastSeq
			}
		}
	} else {
		if prevSeq == 0 || prevSeq >= lastSeq {
			from = lastSeq - size + 1
			if size > lastSeq || size == 0 {
				from = 1
			}
			to = lastSeq
		} else {
			from = prevSeq - size
			if size >= prevSeq || size == 0 {
				from = 1
			}
			to = prevSeq - 1
		}
	}
	return from, to
}
