package webhook_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/webhook"
	"github.com/yorkie-team/yorkie/test/helper"
)

// testRequest is a simple request type for demonstration.
type testRequest struct {
	Name string `json:"name"`
}

// testResponse is a simple response type for demonstration.
type testResponse struct {
	Greeting string `json:"greeting"`
}

// newHMACTestServer creates a new httptest.Server that verifies the HMAC signature.
// It returns a valid JSON response if the signature is correct.
func newHMACTestServer(t *testing.T, validSecret string, responseData testResponse) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		signatureHeader := r.Header.Get("X-Signature-256")
		if signatureHeader == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		if err := helper.VerifySignature(signatureHeader, validSecret, bodyBytes); err != nil {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		assert.NoError(t, json.NewEncoder(w).Encode(responseData))
	}))
}

func newRetryServer(t *testing.T, replyAfter int, responseData testResponse) *httptest.Server {
	var requestCount int32
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := int(atomic.AddInt32(&requestCount, 1))
		if count < replyAfter {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		assert.NoError(t, json.NewEncoder(w).Encode(responseData))
	}))
}

func newDelayServer(t *testing.T, delayTime time.Duration, responseData testResponse) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), delayTime)
		defer cancel()

		select {
		case <-ctx.Done():
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			assert.NoError(t, json.NewEncoder(w).Encode(responseData))
		}
	}))
}
func TestHMAC(t *testing.T) {
	const validSecret = "my-secret-key"
	const invalidSecret = "wrong-key"
	expectedResponse := testResponse{Greeting: "HMAC OK"}

	testServer := newHMACTestServer(t, validSecret, expectedResponse)
	defer testServer.Close()

	client := webhook.NewClient[testRequest, testResponse](webhook.Options{
		MaxRetries:      0,
		MinWaitInterval: 0,
		MaxWaitInterval: 0,
		RequestTimeout:  1 * time.Second,
	})

	t.Run("valid HMAC key test", func(t *testing.T) {
		reqPayload := testRequest{Name: "ValidHMAC"}
		body, err := json.Marshal(reqPayload)
		assert.NoError(t, err)

		resp, statusCode, err := client.Send(context.Background(), testServer.URL, validSecret, body)
		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, statusCode)
		assert.NotNil(t, resp)
		assert.Equal(t, expectedResponse.Greeting, resp.Greeting)
	})

	t.Run("invalid HMAC key test", func(t *testing.T) {
		reqPayload := testRequest{Name: "InvalidHMAC"}
		body, err := json.Marshal(reqPayload)
		assert.NoError(t, err)

		resp, statusCode, err := client.Send(context.Background(), testServer.URL, invalidSecret, body)
		assert.Error(t, err)
		// The server responds with 403 Forbidden if the signature is invalid.
		assert.Equal(t, http.StatusForbidden, statusCode)
		assert.Nil(t, resp)
	})

	t.Run("missing HMAC key test", func(t *testing.T) {
		reqPayload := testRequest{Name: "MissingHMAC"}
		body, err := json.Marshal(reqPayload)
		assert.NoError(t, err)

		resp, statusCode, err := client.Send(context.Background(), testServer.URL, "", body)
		assert.Error(t, err)
		// The server responds with 401 Unauthorized if no signature header is provided.
		assert.Equal(t, http.StatusUnauthorized, statusCode)
		assert.Nil(t, resp)
	})

	t.Run("empty body test", func(t *testing.T) {
		reqPayload := testRequest{}
		body, err := json.Marshal(reqPayload)
		assert.NoError(t, err)

		resp, statusCode, err := client.Send(context.Background(), testServer.URL, validSecret, body)
		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, statusCode)
		assert.NotNil(t, resp)
		assert.Equal(t, expectedResponse.Greeting, resp.Greeting)
	})
}

func TestBackoff(t *testing.T) {
	replyAfter := 4
	reachableRetries := replyAfter - 1
	unreachableRetries := replyAfter - 2
	expectedResponse := testResponse{Greeting: "retry succeed"}
	server := newRetryServer(t, replyAfter, expectedResponse)
	defer server.Close()

	reachableClient := webhook.NewClient[testRequest, testResponse](webhook.Options{
		RequestTimeout:  10 * time.Millisecond,
		MaxRetries:      uint64(reachableRetries),
		MinWaitInterval: 1 * time.Millisecond,
		MaxWaitInterval: 5 * time.Millisecond,
	})

	unreachableClient := webhook.NewClient[testRequest, testResponse](webhook.Options{
		RequestTimeout:  10 * time.Millisecond,
		MaxRetries:      uint64(unreachableRetries),
		MinWaitInterval: 1 * time.Millisecond,
		MaxWaitInterval: 5 * time.Millisecond,
	})

	t.Run("retry fail test", func(t *testing.T) {
		reqPayload := testRequest{Name: "retry fails"}
		body, err := json.Marshal(reqPayload)
		assert.NoError(t, err)

		resp, statusCode, err := unreachableClient.Send(context.Background(), server.URL, "", body)
		assert.Error(t, err)
		assert.ErrorContains(t, err, webhook.ErrWebhookTimeout.Error())
		assert.Equal(t, http.StatusServiceUnavailable, statusCode)
		assert.Nil(t, resp)
	})

	t.Run("retry succeed timeout", func(t *testing.T) {
		reqPayload := testRequest{Name: "retry succeed"}
		body, err := json.Marshal(reqPayload)
		assert.NoError(t, err)

		resp, statusCode, err := reachableClient.Send(context.Background(), server.URL, "", body)
		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, statusCode)
		assert.NotNil(t, resp)
		assert.Equal(t, expectedResponse.Greeting, resp.Greeting)
	})
}

func TestRequestTimeout(t *testing.T) {
	delayTime := 10 * time.Millisecond
	expectedResponse := testResponse{Greeting: "hello"}
	server := newDelayServer(t, delayTime, expectedResponse)
	defer server.Close()

	reachableClient := webhook.NewClient[testRequest, testResponse](webhook.Options{
		RequestTimeout:  15 * time.Millisecond,
		MaxRetries:      0,
		MinWaitInterval: 0,
		MaxWaitInterval: 0,
	})

	unreachableClient := webhook.NewClient[testRequest, testResponse](webhook.Options{
		RequestTimeout:  5 * time.Millisecond,
		MaxRetries:      0,
		MinWaitInterval: 0,
		MaxWaitInterval: 0,
	})

	t.Run("request succeed after timeout", func(t *testing.T) {
		reqPayload := testRequest{Name: "TimeoutTest"}
		body, err := json.Marshal(reqPayload)
		assert.NoError(t, err)

		resp, statusCode, err := reachableClient.Send(context.Background(), server.URL, "", body)
		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, statusCode)
		assert.NotNil(t, resp)
		assert.Equal(t, expectedResponse.Greeting, resp.Greeting)
	})

	t.Run("request fails with timeout test", func(t *testing.T) {
		reqPayload := testRequest{Name: "TimeoutTest"}
		body, err := json.Marshal(reqPayload)
		assert.NoError(t, err)

		resp, statusCode, err := unreachableClient.Send(context.Background(), server.URL, "", body)
		assert.Error(t, err)
		assert.Equal(t, 0, statusCode)
		assert.Nil(t, resp)
	})
}

func TestErrorHandling(t *testing.T) {
	expectedResponse := testResponse{Greeting: "hello"}
	server := newRetryServer(t, 2, expectedResponse)
	defer server.Close()

	unreachableClient := webhook.NewClient[testRequest, testResponse](webhook.Options{
		RequestTimeout:  50 * time.Millisecond,
		MaxRetries:      0,
		MinWaitInterval: 0,
		MaxWaitInterval: 0,
	})

	t.Run("request fails with context done test", func(t *testing.T) {
		reqPayload := testRequest{Name: "ContextDone"}
		body, err := json.Marshal(reqPayload)
		assert.NoError(t, err)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()
		resp, statusCode, err := unreachableClient.Send(ctx, server.URL, "", body)
		assert.Error(t, err)
		assert.Equal(t, http.StatusServiceUnavailable, statusCode)
		assert.Nil(t, resp)
	})

	t.Run("request fails with unreachable url test", func(t *testing.T) {
		reqPayload := testRequest{Name: "invalidURL"}
		body, err := json.Marshal(reqPayload)
		assert.NoError(t, err)

		resp, statusCode, err := unreachableClient.Send(context.Background(), "", "", body)
		assert.Error(t, err)
		assert.Equal(t, 0, statusCode)
		assert.Nil(t, resp)
	})
}
