package types

import "errors"

// Below are the errors may occur depending on the document status.
var (
	// TODO: set error code of types.ErrDocumentNotFound
	ErrDocumentNotFound = errors.New("document not found")
)
