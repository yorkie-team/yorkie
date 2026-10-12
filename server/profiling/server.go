/*
 * Copyright 2021 The Yorkie Authors. All rights reserved.
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
	"context"
	"fmt"
	"net/http"
	"net/http/pprof"
	"path"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/profiling/prometheus"
)

const httpPrefixMetrics = "/metrics"
const httpPrefixPProf = "/debug/pprof"

// cmdlineProfile is the pprof profile Yorkie refuses to serve. It writes
// os.Args, so a credential given as a flag — the admin token signing key, the
// MongoDB connection URI, the cluster secret — would be readable by anyone who
// can reach the profiling port, which carries no authentication of its own.
const cmdlineProfile = "cmdline"

// Server serves information for profiling, such as metrics and pprof information.
type Server struct {
	conf       *Config
	serveMux   *http.ServeMux
	httpServer *http.Server
}

// NewServer creates an instance of Server.
func NewServer(conf *Config, metrics *prometheus.Metrics) *Server {
	serveMux := http.NewServeMux()
	if conf.PprofEnabled {
		serveMux.Handle(httpPrefixPProf+"/", withoutCmdline(http.HandlerFunc(pprof.Index)))
		serveMux.Handle(httpPrefixPProf+"/profile", http.HandlerFunc(pprof.Profile))
		serveMux.Handle(httpPrefixPProf+"/symbol", http.HandlerFunc(pprof.Symbol))
		serveMux.Handle(httpPrefixPProf+"/"+cmdlineProfile, http.HandlerFunc(refuseCmdline))
		serveMux.Handle(httpPrefixPProf+"/trace", http.HandlerFunc(pprof.Trace))
		serveMux.Handle(httpPrefixPProf+"/heap", pprof.Handler("heap"))
		serveMux.Handle(httpPrefixPProf+"/goroutine", pprof.Handler("goroutine"))
		serveMux.Handle(httpPrefixPProf+"/threadcreate", pprof.Handler("threadcreate"))
		serveMux.Handle(httpPrefixPProf+"/block", pprof.Handler("block"))
		serveMux.Handle(httpPrefixPProf+"/mutex", pprof.Handler("mutex"))
	}

	if metrics != nil {
		serveMux.Handle(httpPrefixMetrics, promhttp.HandlerFor(metrics.Registry(), promhttp.HandlerOpts{}))
	}

	return &Server{
		conf:     conf,
		serveMux: serveMux,
		httpServer: &http.Server{
			Addr:              fmt.Sprintf(":%d", conf.Port),
			ReadHeaderTimeout: 5 * time.Second, // Slowloris protection (gosec G112)
		},
	}
}

// withoutCmdline refuses any request that resolves to the cmdline profile
// before next sees it. next is the pprof index, which is the subtree fallback
// for every path the exact patterns miss, so a variant such as a trailing
// slash or a percent-escaped name reaches it rather than the pattern above.
func withoutCmdline(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isCmdlinePath(r.URL.Path) {
			refuseCmdline(w, r)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// isCmdlinePath reports whether p addresses the cmdline profile. The path is
// normalized first: http.ServeMux redirects dot segments and repeated slashes
// rather than rejecting them, so a handler can still be reached through them.
func isCmdlinePath(p string) bool {
	return path.Clean("/"+p) == httpPrefixPProf+"/"+cmdlineProfile
}

// refuseCmdline answers a request for the cmdline profile. It replies as the
// pprof package does for a profile it does not know, so that the route reads
// as absent rather than as present and guarded.
func refuseCmdline(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	if _, err := fmt.Fprintln(w, "Unknown profile"); err != nil {
		logging.DefaultLogger().Errorf("write cmdline refusal: %v", err)
	}
}

// Handle handles the given handler.
func (s *Server) Handle(pattern string, handler http.Handler) {
	s.serveMux.Handle(pattern, handler)
}

// Start starts the server.
func (s *Server) Start() error {
	return s.listenAndServe()
}

// Shutdown shut down the server.
func (s *Server) Shutdown(graceful bool) {
	if graceful {
		if err := s.httpServer.Shutdown(context.Background()); err != nil {
			logging.DefaultLogger().Error("HTTP server Shutdown: %v", err)
		}
		return
	}

	if err := s.httpServer.Close(); err != nil {
		logging.DefaultLogger().Error("HTTP server close: %v", err)
	}
}

func (s *Server) listenAndServe() error {
	go func() {
		logging.DefaultLogger().Infof(fmt.Sprintf("serving profiling on %d", s.conf.Port))
		s.httpServer.Handler = s.serveMux
		if err := s.httpServer.ListenAndServe(); err != http.ErrServerClosed {
			logging.DefaultLogger().Errorf("HTTP server ListenAndServe: %v", err)
		}
	}()
	return nil
}
