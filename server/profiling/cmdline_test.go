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
func get(t *testing.T, mux *http.ServeMux, path string) (int, string) {
	t.Helper()

	for range 2 {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		location := rec.Header().Get("Location")
		if rec.Code < 300 || rec.Code >= 400 || location == "" {
			return rec.Code, rec.Body.String()
		}
		path = location
	}

	t.Fatalf("%s redirected more than once", path)
	return 0, ""
}

// cmdlinePaths are the ways a caller can ask the profiling mux for the
// command line: the documented route, the forms http.ServeMux normalizes
// onto it, and the forms that fall through to the pprof index handler.
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
				status, body := get(t, mux, path)
				assert.NotEqual(t, http.StatusOK, status, "%s must not succeed", path)
				for _, sentinel := range sentinels() {
					assert.NotContains(t, body, sentinel, "%s disclosed a credential", path)
				}
			}
		})
	}
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
		status, body := get(t, mux, path)
		assert.Equal(t, http.StatusOK, status, "%s must stay available", path)
		assert.NotEmpty(t, body, "%s returned an empty body", path)
		for _, sentinel := range sentinels() {
			assert.NotContains(t, body, sentinel, "%s disclosed a credential", path)
		}
	}
}

func TestMetricsWithoutPprof(t *testing.T) {
	metrics, err := prometheus.NewMetrics()
	assert.NoError(t, err)
	mux := NewServer(&Config{Port: 0, PprofEnabled: false}, metrics).serveMux

	status, body := get(t, mux, "/metrics")
	assert.Equal(t, http.StatusOK, status)
	assert.True(t, strings.Contains(body, "# HELP"), "metrics body is not an exposition")
}
