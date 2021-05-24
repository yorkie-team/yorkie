package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/log"
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
	if len(be.Config.AuthorizationWebhook) == 0 {
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
		be.Config.AuthorizationWebhook,
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

	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var authResp types.AuthWebhookResponse
	if err = json.Unmarshal(respBody, &authResp); err != nil {
		return err
	}

	if !authResp.Allowed {
		return fmt.Errorf("%s: %w", authResp.Reason, ErrNotAllowed)
	}

	return nil
}
