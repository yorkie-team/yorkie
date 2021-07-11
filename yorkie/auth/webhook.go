package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/yorkie-team/yorkie/internal/log"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/types"
	"github.com/yorkie-team/yorkie/yorkie/backend"
)

var (
	// ErrNotAllowed is returned when the given user is not allowed for the access.
	ErrNotAllowed = errors.New("method is not allowed for this user")

	// ErrUnexpectedStatusCode is returned when the response code is not 200 from the webhook.
	ErrUnexpectedStatusCode = errors.New("unexpected status code from webhook")
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

	return withExponentialBackoff(be.Config, func() (int, error) {
		resp, err := http.Post(
			be.Config.AuthorizationWebhookURL,
			"application/json",
			bytes.NewBuffer(reqBody),
		)
		if err != nil {
			return 0, err
		}

		defer func() {
			if err := resp.Body.Close(); err != nil {
				log.Logger.Error(err)
			}
		}()

		if http.StatusOK != resp.StatusCode {
			return resp.StatusCode, fmt.Errorf("%d: %w", resp.StatusCode, ErrUnexpectedStatusCode)
		}

		authResp, err := types.NewAuthWebhookResponse(resp.Body)
		if err != nil {
			return resp.StatusCode, err
		}

		if !authResp.Allowed {
			return resp.StatusCode, fmt.Errorf("%s: %w", authResp.Reason, ErrNotAllowed)
		}

		return resp.StatusCode, nil
	})
}

func withExponentialBackoff(config *backend.Config, webhookFn func() (int, error)) error {
	statusCode, err := webhookFn()
	if !shouldRetry(statusCode) {
		return err
	}

	var retries uint64
	for {
		if retries == config.AuthorizationWebhookMaxRetries {
			return err
		}

		time.Sleep(waitInterval(retries, config.AuthorizationWebhookMaxWaitInterval))

		statusCode, err := webhookFn()
		if !shouldRetry(statusCode) {
			return err
		}
		retries++
	}
}

func waitInterval(retries uint64, maxWaitInterval uint64) time.Duration {
	interval := math.Pow(2, float64(retries)) * 100
	interval = math.Min(interval, float64(maxWaitInterval))
	return time.Duration(int64(interval)) * time.Millisecond
}

func shouldRetry(statusCode int) bool {
	return statusCode == http.StatusRequestTimeout ||
		statusCode == http.StatusInternalServerError ||
		statusCode == http.StatusBadGateway ||
		statusCode == http.StatusServiceUnavailable
}
