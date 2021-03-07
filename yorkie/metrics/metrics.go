package metrics

import "github.com/yorkie-team/yorkie/yorkie/metrics/prometheus"

type metrics struct {
	Server    ServerMetrics
	RPCServer RPCServerMetrics
	DB        DBMetrics
}

func newMetrics() *metrics {
	return &metrics{
		Server:    prometheus.NewServerMetrics(),
		RPCServer: prometheus.NewRPCServerMetrics(),
		DB:        prometheus.NewDBMetrics(),
	}
}

type ServerMetrics interface {
	WithServerVersion(version string)
}

type RPCServerMetrics interface {
	ObservePushpullResponseSeconds(seconds float64)
	AddPushpullReceivedChanges(count float64)
	AddPushpullSentChanges(count float64)
	AddPushpullSnapshotBytes(byte float64)
}

type DBMetrics interface {
	ObservePushpullSnapshotDurationSeconds(seconds float64)
}
