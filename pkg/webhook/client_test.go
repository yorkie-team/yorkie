package webhook_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/cache"
	pkgtypes "github.com/yorkie-team/yorkie/pkg/types"
	"github.com/yorkie-team/yorkie/pkg/webhook"
)

// testRequest is a simple request type for demonstration.
type testRequest struct {
	Name string `json:"name"`
}

// testResponse is a simple response type for demonstration.
type testResponse struct {
	Greeting string `json:"greeting"`
}

func verifySignature(signatureHeader, secret string, body []byte) error {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expectedSig := hex.EncodeToString(mac.Sum(nil))
	expectedSigHeader := fmt.Sprintf("sha256=%s", expectedSig)
	if !hmac.Equal([]byte(signatureHeader), []byte(expectedSigHeader)) {
		return errors.New("signature validation failed")
	}

	return nil
}

func TestHMAC(t *testing.T) {
	const secretKey = "my-secret-key"
	const wrongKey = "wrong-key"
	resData := testResponse{Greeting: "HMAC OK"}

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		signatureHeader := r.Header.Get("X-Signature-256")
		if signatureHeader == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		if err := verifySignature(signatureHeader, secretKey, bodyBytes); err != nil {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
		assert.NoError(t, json.NewEncoder(w).Encode(resData))
	}))
	defer testServer.Close()
	client := webhook.NewClient[testRequest, testResponse](
		cache.NewLRUExpireCache[string, pkgtypes.Pair[int, *testResponse]](
			100,
		),
		webhook.Options{
			CacheTTL:        10 * time.Second,
			MaxRetries:      0,
			MinWaitInterval: 2 * time.Second,
			MaxWaitInterval: 10 * time.Second,
			RequestTimeout:  30 * time.Second,
		},
	)
	t.Run("webhook client with valid HMAC key test", func(t *testing.T) {
		resp, statusCode, err := client.Send(
			context.Background(),
			":auth",
			testServer.URL,
			secretKey,
			testRequest{Name: t.Name()},
		)
		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, statusCode)
		assert.NotNil(t, resp)
		assert.Equal(t, resData.Greeting, resp.Greeting)
	})

	t.Run("webhook client with invalid HMAC key test", func(t *testing.T) {
		resp, statusCode, err := client.Send(
			context.Background(),
			":auth",
			testServer.URL,
			wrongKey,
			testRequest{Name: t.Name()},
		)
		assert.Error(t, err)
		assert.Equal(t, http.StatusForbidden, statusCode)
		assert.Nil(t, resp)
	})

	t.Run("webhook client without HMAC key test", func(t *testing.T) {
		resp, statusCode, err := client.Send(
			context.Background(),
			":auth",
			testServer.URL,
			"",
			testRequest{Name: t.Name()},
		)
		assert.Error(t, err)
		assert.Equal(t, http.StatusUnauthorized, statusCode)
		assert.Nil(t, resp)
	})

	t.Run("webhook client with empty body test", func(t *testing.T) {
		resp, statusCode, err := client.Send(
			context.Background(),
			":auth",
			testServer.URL,
			secretKey,
			testRequest{},
		)
		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, statusCode)
		assert.NotNil(t, resp)
		assert.Equal(t, resData.Greeting, resp.Greeting)
	})
}
