package metrics

import (
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/yorkie-team/yorkie/pkg/log"
	"github.com/yorkie-team/yorkie/pkg/version"
)

type Config struct {
	Port int
}

type Server struct {
	conf *Config
}

func NewServer(conf *Config) (*Server, error) {
	return &Server{
		conf: conf,
	}, nil
}

func (s *Server) listenAndServe() error {
	go func() {
		http.Handle("/metrics", promhttp.Handler())
		if err := http.ListenAndServe(fmt.Sprintf(":%d", s.conf.Port), nil); err != nil {
			log.Logger.Error(err)
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

func (s *Server) Start() error {
	recordMetrics()
	return s.listenAndServe()
}
