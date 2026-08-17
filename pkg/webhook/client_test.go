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
	"github.com/stretchr/testify/require"

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
		// NOTE(hackerwins): Answer only once the delay has actually elapsed. A
		// timer derived from r.Context() also fires when the client gives up,
		// which made this server answer 200 the instant a caller cancelled --
		// so a test asserting the request times out raced the response it was
		// supposed to never receive.
		select {
		case <-time.After(delayTime):
		case <-r.Context().Done():
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		assert.NoError(t, json.NewEncoder(w).Encode(responseData))
	}))
}

func TestHMAC(t *testing.T) {
	const validSecret = "my-secret-key"
	const invalidSecret = "wrong-key"
	expectedResponse := testResponse{Greeting: "HMAC OK"}

	testServer := newHMACTestServer(t, validSecret, expectedResponse)
	defer testServer.Close()

	client := webhook.NewClient[testRequest, testResponse](false)
	options := webhook.Options{
		MaxRetries:      0,
		MinWaitInterval: 0,
		MaxWaitInterval: 0,
		RequestTimeout:  1 * time.Second,
	}

	t.Run("valid HMAC key test", func(t *testing.T) {
		reqPayload := testRequest{Name: "ValidHMAC"}
		body, err := json.Marshal(reqPayload)
		assert.NoError(t, err)

		resp, statusCode, err := client.Send(context.Background(), testServer.URL, validSecret, body, options)
		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, statusCode)
		assert.NotNil(t, resp)
		assert.Equal(t, expectedResponse.Greeting, resp.Greeting)
	})

	t.Run("invalid HMAC key test", func(t *testing.T) {
		reqPayload := testRequest{Name: "InvalidHMAC"}
		body, err := json.Marshal(reqPayload)
		assert.NoError(t, err)

		resp, statusCode, err := client.Send(context.Background(), testServer.URL, invalidSecret, body, options)
		assert.Error(t, err)
		// The server responds with 403 Forbidden if the signature is invalid.
		assert.Equal(t, http.StatusForbidden, statusCode)
		assert.Nil(t, resp)
	})

	t.Run("missing HMAC key test", func(t *testing.T) {
		reqPayload := testRequest{Name: "MissingHMAC"}
		body, err := json.Marshal(reqPayload)
		assert.NoError(t, err)

		resp, statusCode, err := client.Send(context.Background(), testServer.URL, "", body, options)
		assert.Error(t, err)
		// The server responds with 401 Unauthorized if no signature header is provided.
		assert.Equal(t, http.StatusUnauthorized, statusCode)
		assert.Nil(t, resp)
	})

	t.Run("empty body test", func(t *testing.T) {
		reqPayload := testRequest{}
		body, err := json.Marshal(reqPayload)
		assert.NoError(t, err)

		resp, statusCode, err := client.Send(context.Background(), testServer.URL, validSecret, body, options)
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

	// NOTE(hackerwins): These subtests are about the retry loop, not about
	// per-request deadlines: ErrWebhookTimeout means the retries ran out while
	// the server was still answering 503. A per-request budget tight enough to
	// expire turns that into a bare "context deadline exceeded" and fails the
	// assertion, so keep it far above what newRetryServer needs -- it answers
	// immediately, and an unused budget costs nothing.
	const requestTimeout = 5 * time.Second

	webhookClient := webhook.NewClient[testRequest, testResponse](false)
	t.Run("retry fail test", func(t *testing.T) {
		reqPayload := testRequest{Name: "retry fails"}
		body, err := json.Marshal(reqPayload)
		assert.NoError(t, err)
		unreachableRetriesOptions := webhook.Options{
			MaxRetries:      uint64(unreachableRetries),
			MinWaitInterval: 1 * time.Millisecond,
			MaxWaitInterval: 5 * time.Millisecond,
			RequestTimeout:  requestTimeout,
		}
		resp, statusCode, err := webhookClient.Send(context.Background(), server.URL, "", body, unreachableRetriesOptions)
		assert.Error(t, err)
		assert.ErrorContains(t, err, webhook.ErrWebhookTimeout.Error())
		assert.Equal(t, http.StatusServiceUnavailable, statusCode)
		assert.Nil(t, resp)
	})

	t.Run("retry succeed timeout", func(t *testing.T) {
		reqPayload := testRequest{Name: "retry succeed"}
		body, err := json.Marshal(reqPayload)
		assert.NoError(t, err)
		reachableRetriesOptions := webhook.Options{
			MaxRetries:      uint64(reachableRetries),
			MinWaitInterval: 1 * time.Millisecond,
			MaxWaitInterval: 5 * time.Millisecond,
			RequestTimeout:  requestTimeout,
		}
		resp, statusCode, err := webhookClient.Send(context.Background(), server.URL, "", body, reachableRetriesOptions)
		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, statusCode)
		assert.NotNil(t, resp)
		assert.Equal(t, expectedResponse.Greeting, resp.Greeting)
	})
}

func TestRequestTimeout(t *testing.T) {
	// NOTE(hackerwins): The two subtests race the client's budget against the
	// server's delay in opposite directions, so each gets its own server.
	// Sharing one delay leaves whichever side sits closer to it decided by
	// scheduler latency, which is what made both flaky.
	//
	// The margins are bought differently. Widening the budget is free -- the
	// responding server answers after replyDelay either way -- so the success
	// case takes seconds of headroom. Widening the delay is not: httptest's
	// Close waits for the outstanding handler, so stallDelay is the price of
	// every run. 200ms against a 5ms budget is a 40x margin for the cost of
	// 200ms.
	const replyDelay = 10 * time.Millisecond
	const stallDelay = 200 * time.Millisecond
	const giveUpAfter = 5 * time.Millisecond
	expectedResponse := testResponse{Greeting: "hello"}

	respondingServer := newDelayServer(t, replyDelay, expectedResponse)
	defer respondingServer.Close()
	stallingServer := newDelayServer(t, stallDelay, expectedResponse)
	defer stallingServer.Close()

	webhookClient := webhook.NewClient[testRequest, testResponse](false)
	t.Run("request succeed after timeout", func(t *testing.T) {
		reqPayload := testRequest{Name: "TimeoutTest"}
		body, err := json.Marshal(reqPayload)
		assert.NoError(t, err)
		options := webhook.Options{
			MaxRetries:      0,
			MinWaitInterval: 0,
			MaxWaitInterval: 0,
			RequestTimeout:  5 * time.Second,
		}
		resp, statusCode, err := webhookClient.Send(context.Background(), respondingServer.URL, "", body, options)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, statusCode)
		require.NotNil(t, resp)
		assert.Equal(t, expectedResponse.Greeting, resp.Greeting)
	})

	t.Run("request fails with timeout test", func(t *testing.T) {
		reqPayload := testRequest{Name: "TimeoutTest"}
		body, err := json.Marshal(reqPayload)
		assert.NoError(t, err)
		options := webhook.Options{
			MaxRetries:      0,
			MinWaitInterval: 0,
			MaxWaitInterval: 0,
			RequestTimeout:  giveUpAfter,
		}
		// The client gives up milliseconds in while the server is still seconds
		// from answering, and the server drops the request the moment it is
		// cancelled, so no response can arrive late and satisfy the call.
		resp, statusCode, err := webhookClient.Send(context.Background(), stallingServer.URL, "", body, options)
		assert.Error(t, err)
		assert.Equal(t, 0, statusCode)
		assert.Nil(t, resp)
	})
}

func TestErrorHandling(t *testing.T) {
	expectedResponse := testResponse{Greeting: "hello"}
	server := newRetryServer(t, 2, expectedResponse)
	defer server.Close()

	options := webhook.Options{
		MaxRetries:      0,
		MinWaitInterval: 0,
		MaxWaitInterval: 0,
		RequestTimeout:  5 * time.Second,
	}
	unreachableClient := webhook.NewClient[testRequest, testResponse](false)

	t.Run("request fails with context done test", func(t *testing.T) {
		reqPayload := testRequest{Name: "ContextDone"}
		body, err := json.Marshal(reqPayload)
		assert.NoError(t, err)

		// NOTE(hackerwins): Despite the name, what this asserts is retry
		// exhaustion -- retry returns (0, ctx.Err()) when the context wins, not
		// the 503 expected below, so the outcome checked here is the one where
		// the context does NOT fire. The deadline was tight enough that it
		// sometimes did, which failed the test; it is now wide enough that the
		// asserted path always wins. Making the context genuinely decide the
		// outcome would mean asserting a different result.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		resp, statusCode, err := unreachableClient.Send(ctx, server.URL, "", body, options)
		assert.Error(t, err)
		assert.Equal(t, http.StatusServiceUnavailable, statusCode)
		assert.Nil(t, resp)
	})

	t.Run("request fails with unreachable url test", func(t *testing.T) {
		reqPayload := testRequest{Name: "invalidURL"}
		body, err := json.Marshal(reqPayload)
		assert.NoError(t, err)

		resp, statusCode, err := unreachableClient.Send(context.Background(), "", "", body, options)
		assert.Error(t, err)
		assert.Equal(t, 0, statusCode)
		assert.Nil(t, resp)
	})

	t.Run("invalid JSON response test", func(t *testing.T) {
		invalidJSONServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte("invalid json response"))
			assert.NoError(t, err)
		}))
		defer invalidJSONServer.Close()

		reqPayload := testRequest{Name: "test"}
		body, err := json.Marshal(reqPayload)
		assert.NoError(t, err)

		options := webhook.Options{
			MaxRetries:      0,
			MinWaitInterval: 0,
			MaxWaitInterval: 0,
			// The server answers immediately; this asserts how its body is
			// decoded, so the budget only has to be wide enough never to be
			// what fails the call.
			RequestTimeout: 5 * time.Second,
		}
		resp, statusCode, err := unreachableClient.Send(context.Background(), invalidJSONServer.URL, "", body, options)
		assert.Error(t, err)
		assert.Equal(t, http.StatusOK, statusCode)
		assert.Nil(t, resp)
		assert.ErrorIs(t, err, webhook.ErrInvalidJSONResponse)
	})
}
