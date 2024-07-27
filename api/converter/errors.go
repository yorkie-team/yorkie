package converter

import (
	"errors"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
)

// ErrorCodeOf returns the error code of the given error.
func ErrorCodeOf(err error) string {
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		return ""
	}
	for _, detail := range connectErr.Details() {
		msg, valueErr := detail.Value()
		if valueErr != nil {
			continue
		}

		if errorInfo, ok := msg.(*errdetails.ErrorInfo); ok {
			return errorInfo.GetMetadata()["code"]
		}
	}
	return ""
}
