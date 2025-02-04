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
	"github.com/yorkie-team/yorkie/pkg/types"
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
	reqData := testRequest{Name: "HMAC Tester"}
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

	t.Run("webhook client with valid HMAC key test", func(t *testing.T) {
		client := webhook.NewClient[testRequest, testResponse](
			testServer.URL,
			cache.NewLRUExpireCache[string, types.Pair[int, *testResponse]](100),
			webhook.Options{
				CacheKeyPrefix:  "testPrefix-hmac",
				CacheTTL:        5 * time.Second,
				MaxRetries:      0,
				MaxWaitInterval: 200 * time.Millisecond,
				HMACKey:         secretKey,
			},
		)

		ctx := context.Background()
		resp, statusCode, err := client.Send(ctx, reqData)
		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, statusCode)
		assert.NotNil(t, resp)
		assert.Equal(t, resData.Greeting, resp.Greeting)
	})

	t.Run("webhook client with invalid HMAC key test", func(t *testing.T) {
		client := webhook.NewClient[testRequest, testResponse](
			testServer.URL,
			cache.NewLRUExpireCache[string, types.Pair[int, *testResponse]](100),
			webhook.Options{
				CacheKeyPrefix:  "testPrefix-hmac",
				CacheTTL:        5 * time.Second,
				MaxRetries:      0,
				MaxWaitInterval: 200 * time.Millisecond,
				HMACKey:         wrongKey,
			},
		)

		ctx := context.Background()
		resp, statusCode, err := client.Send(ctx, reqData)
		assert.Error(t, err)
		assert.Equal(t, http.StatusForbidden, statusCode)
		assert.Nil(t, resp)
	})

	t.Run("webhook client without HMAC key test", func(t *testing.T) {
		client := webhook.NewClient[testRequest](
			testServer.URL,
			cache.NewLRUExpireCache[string, types.Pair[int, *testResponse]](100),
			webhook.Options{
				CacheKeyPrefix:  "testPrefix-hmac",
				CacheTTL:        5 * time.Second,
				MaxRetries:      0,
				MaxWaitInterval: 200 * time.Millisecond,
			},
		)

		ctx := context.Background()
		resp, statusCode, err := client.Send(ctx, reqData)
		assert.Error(t, err)
		assert.Equal(t, http.StatusUnauthorized, statusCode)
		assert.Nil(t, resp)
	})

	t.Run("webhook client with empty body test", func(t *testing.T) {
		client := webhook.NewClient[testRequest](
			testServer.URL,
			cache.NewLRUExpireCache[string, types.Pair[int, *testResponse]](100),
			webhook.Options{
				CacheKeyPrefix:  "testPrefix-hmac",
				CacheTTL:        5 * time.Second,
				MaxRetries:      0,
				MaxWaitInterval: 200 * time.Millisecond,
				HMACKey:         secretKey,
			},
		)

		ctx := context.Background()
		resp, statusCode, err := client.Send(ctx, testRequest{})
		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, statusCode)
		assert.NotNil(t, resp)
		assert.Equal(t, resData.Greeting, resp.Greeting)
	})
}
