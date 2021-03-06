package prometheus

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestServerMetrics(t *testing.T) {
	var serverMetrics = NewServerMetrics()

	t.Run("with server version test", func(t *testing.T) {
		serverMetrics.WithServerVersion("1.0.0")

		expected := `
			# HELP yorkie_server_version Which version is running. 1 for 'server_version' label with current version.
            # TYPE yorkie_server_version gauge
            yorkie_server_version{server_version="1.0.0"} 1
		`
		if err := testutil.CollectAndCompare(serverMetrics.currentVersion, strings.NewReader(expected)); err != nil {
			t.Errorf("unexpected collecting result:\n%s", err)
		}
	})
}
