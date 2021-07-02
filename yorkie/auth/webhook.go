package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/yorkie-team/yorkie/internal/log"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/types"
	"github.com/yorkie-team/yorkie/yorkie/backend"
)

var (
	// ErrNotAllowed is returned when the given user is not allowed for the access.
	ErrNotAllowed = errors.New("method is not allowed for this user")
)

// AccessAttributes returns an array of AccessAttribute from the given pack.
func AccessAttributes(pack *change.Pack) []types.AccessAttribute {
	verb := types.Read
	if pack.HasChanges() {
		verb = types.ReadWrite
	}

	// NOTE(hackerwins): In the future, methods such as bulk PushPull can be
	// added, so we declare it as an array.
	return []types.AccessAttribute{{
		Key:  pack.DocumentKey.BSONKey(),
		Verb: verb,
	}}
}

// VerifyAccess verifies the given access.
func VerifyAccess(ctx context.Context, be *backend.Backend, info *types.AccessInfo) error {
	if !be.Config.RequireAuth(info.Method) {
		return nil
	}

	reqBody, err := json.Marshal(types.AuthWebhookRequest{
		Token:      TokenFromCtx(ctx),
		Method:     info.Method,
		Attributes: info.Attributes,
	})
	if err != nil {
		return err
	}

	// TODO(hackerwins): We need to apply retryBackoff in case of failure
	resp, err := http.Post(
		be.Config.AuthorizationWebhookURL,
		"application/json",
		bytes.NewBuffer(reqBody),
	)
	if err != nil {
		return err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Logger.Error(err)
		}
	}()

	authResp, err := types.NewAuthWebhookResponse(resp.Body)
	if err != nil {
		return err
	}

	if !authResp.Allowed {
		return fmt.Errorf("%s: %w", authResp.Reason, ErrNotAllowed)
	}

	return nil
}
