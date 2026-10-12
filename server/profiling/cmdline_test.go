/*
 * Copyright 2026 The Yorkie Authors. All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package profiling

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/server/profiling/prometheus"
)

// The credentials a deployment may hand the server as flags. These are
// made-up values planted in a copy of os.Args; the tests never read another
// process and always put the real os.Args back.
const (
	sentinelSecretKey = "SENTINEL-admin-signing-key-ca7b1f"
	sentinelMongoURI  = "mongodb://SENTINEL-mongo-user:SENTINEL-mongo-pw-9e2d04@db:27017" // #nosec G101
	sentinelCluster   = "SENTINEL-cluster-secret-40ba66"
)

func sentinels() []string {
	return []string{sentinelSecretKey, sentinelMongoURI, sentinelCluster}
}

// plantCredentialArgs puts the sentinels into os.Args for the duration of the
// test and restores the original arguments afterwards.
func plantCredentialArgs(t *testing.T) {
	t.Helper()

	original := os.Args
	t.Cleanup(func() { os.Args = original })

	os.Args = []string{
		"yorkie", "server",
		"--backend-secret-key", sentinelSecretKey,
		"--mongo-connection-uri", sentinelMongoURI,
		"--cluster-secret", sentinelCluster,
		"--pprof-enabled",
	}
}

// get serves path through the given mux, following a single redirect so that
// a path http.ServeMux cleans (repeated slashes, dot segments) is judged by
// the handler it ends up at rather than by the redirect.
func get(t *testing.T, mux *http.ServeMux, path string) *httptest.ResponseRecorder {
	t.Helper()

	for range 2 {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		location := rec.Header().Get("Location")
		if rec.Code < 300 || rec.Code >= 400 || location == "" {
			return rec
		}
		path = location
	}

	t.Fatalf("%s redirected more than once", path)
	return nil
}

// assertRefusal checks the contract refuseCmdline documents: the status, body
// and headers pprof itself uses for a profile it does not know.
func assertRefusal(t *testing.T, path string, rec *httptest.ResponseRecorder) {
	t.Helper()

	assert.Equal(t, http.StatusNotFound, rec.Code, "%s must be refused", path)
	assert.Equal(t, "Unknown profile\n", rec.Body.String(), "%s refusal body", path)
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"), "%s refusal headers", path)
	assert.Equal(t, "text/plain; charset=utf-8", rec.Header().Get("Content-Type"), "%s refusal headers", path)
}

// cmdlinePaths are the ways a caller can ask the profiling mux for the
// command line: the documented route, the forms http.ServeMux normalizes
// onto it, and the forms it routes to the pprof index subtree instead.
var cmdlinePaths = []string{
	"/debug/pprof/cmdline",
	"/debug/pprof/cmdline/",
	"/debug/pprof/cmdline?debug=1",
	"/debug/pprof//cmdline",
	"/debug/pprof/heap/../cmdline",
	"/debug/pprof/%63mdline",
	"/debug/pprof/cmdline/extra",
}

func TestPprofCmdlineIsRefused(t *testing.T) {
	plantCredentialArgs(t)

	for _, enabled := range []bool{true, false} {
		name := "pprof disabled"
		if enabled {
			name = "pprof enabled"
		}

		t.Run(name, func(t *testing.T) {
			mux := NewServer(&Config{Port: 0, PprofEnabled: enabled}, nil).serveMux

			for _, path := range cmdlinePaths {
				rec := get(t, mux, path)
				assert.NotEqual(t, http.StatusOK, rec.Code, "%s must not succeed", path)
				for _, sentinel := range sentinels() {
					assert.NotContains(t, rec.Body.String(), sentinel, "%s disclosed a credential", path)
				}
			}
		})
	}
}

// TestCmdlineGuardRefusesBeforeIndex exercises withoutCmdline on its own. The
// wrapped handler stands in for pprof.Index, the subtree fallback that sees
// every cmdline spelling the exact pattern misses; the guard must refuse those
// without reaching it, and must let every other profile through untouched.
func TestCmdlineGuardRefusesBeforeIndex(t *testing.T) {
	for _, tc := range []struct {
		path    string
		refused bool
	}{
		{path: "/debug/pprof/cmdline", refused: true},
		{path: "/debug/pprof/cmdline/", refused: true},
		{path: "/debug/pprof//cmdline", refused: true},
		{path: "/debug/pprof/heap/../cmdline", refused: true},
		{path: "/debug/pprof/cmdline/.", refused: true},
		{path: "/debug/pprof/", refused: false},
		{path: "/debug/pprof/heap", refused: false},
		{path: "/debug/pprof/cmdline/extra", refused: false},
		{path: "/debug/pprof/cmdlines", refused: false},
	} {
		reached := false
		next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			reached = true
			w.WriteHeader(http.StatusOK)
		})

		rec := httptest.NewRecorder()
		withoutCmdline(next).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))

		if !tc.refused {
			assert.True(t, reached, "%s must reach the pprof index", tc.path)
			continue
		}

		assert.False(t, reached, "%s reached the pprof index", tc.path)
		assertRefusal(t, tc.path, rec)
	}
}

// TestCmdlineGuardIsWiredToIndexSubtree pins the guard onto the mux. A
// trailing-slash cmdline is routed to the index subtree rather than to the
// exact cmdline pattern, and pprof marks its own errors with X-Go-Pprof, so a
// refusal without that header can only have come from withoutCmdline.
func TestCmdlineGuardIsWiredToIndexSubtree(t *testing.T) {
	plantCredentialArgs(t)
	mux := NewServer(&Config{Port: 0, PprofEnabled: true}, nil).serveMux

	const path = httpPrefixPProf + "/" + cmdlineProfile + "/"
	_, pattern := mux.Handler(httptest.NewRequest(http.MethodGet, path, nil))
	assert.Equal(t, httpPrefixPProf+"/", pattern, "%s no longer routes to the index subtree", path)

	rec := get(t, mux, path)
	assertRefusal(t, path, rec)
	assert.Empty(t, rec.Header().Get("X-Go-Pprof"), "%s was answered by pprof, not by the guard", path)
}

func TestPprofRoutesSurvive(t *testing.T) {
	plantCredentialArgs(t)

	metrics, err := prometheus.NewMetrics()
	assert.NoError(t, err)
	mux := NewServer(&Config{Port: 0, PprofEnabled: true}, metrics).serveMux

	for _, path := range []string{
		"/debug/pprof/heap?debug=1",
		"/debug/pprof/goroutine?debug=1",
		"/debug/pprof/block?debug=1",
		"/debug/pprof/mutex?debug=1",
		"/debug/pprof/threadcreate?debug=1",
		"/debug/pprof/",
		"/metrics",
	} {
		rec := get(t, mux, path)
		assert.Equal(t, http.StatusOK, rec.Code, "%s must stay available", path)
		assert.NotEmpty(t, rec.Body.String(), "%s returned an empty body", path)
		for _, sentinel := range sentinels() {
			assert.NotContains(t, rec.Body.String(), sentinel, "%s disclosed a credential", path)
		}
	}
}

func TestMetricsWithoutPprof(t *testing.T) {
	metrics, err := prometheus.NewMetrics()
	assert.NoError(t, err)
	mux := NewServer(&Config{Port: 0, PprofEnabled: false}, metrics).serveMux

	rec := get(t, mux, "/metrics")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, strings.Contains(rec.Body.String(), "# HELP"), "metrics body is not an exposition")
}
