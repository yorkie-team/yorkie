package metrics

import (
	"context"
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/yorkie-team/yorkie/pkg/log"
	"github.com/yorkie-team/yorkie/pkg/version"
)

// Config exposed port
type Config struct {
	Port int
}

// Server config and http server
type Server struct {
	conf          *Config
	metricsServer *http.Server
}

// NewServer create new http server
func NewServer(conf *Config) (*Server, error) {
	return &Server{
		conf: conf,
		metricsServer: &http.Server{
			Addr: fmt.Sprintf(":%d", conf.Port),
		},
	}, nil
}

func (s *Server) listenAndServe() error {
	go func() {
		log.Logger.Infof(fmt.Sprintf("serving Metrics on %d", s.conf.Port))
		http.Handle("/metrics", promhttp.Handler())
		if err := s.metricsServer.ListenAndServe(); err != http.ErrServerClosed {
			log.Logger.Error("HTTP server ListenAndServe: %v", err)
		}
	}()
	return nil
}

var (
	currentVersion = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "yorkie",
		Subsystem: "server",
		Name:      "version",
		Help:      "Which version is running. 1 for 'server_version' label with current version.",
	}, []string{"server_version"})
)

func recordMetrics() {
	prometheus.MustRegister(currentVersion)

	currentVersion.With(prometheus.Labels{
		"server_version": version.Version,
	}).Set(1)
}

// Start Register application specific metrics and start the http server.
func (s *Server) Start() error {
	recordMetrics()
	return s.listenAndServe()
}

// Shutdown shutdown or close server
func (s *Server) Shutdown(graceful bool) {
	if graceful {
		if err := s.metricsServer.Shutdown(context.Background()); err != nil {
			log.Logger.Error("HTTP server Shutdown: %v", err)
		}
	} else {
		if err := s.metricsServer.Close(); err != nil {
			log.Logger.Error("HTTP server Close: %v", err)
		}
	}
}
