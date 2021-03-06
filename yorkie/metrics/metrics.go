package metrics

type RPCServer interface {
	ObservePushpullResponseSeconds(seconds float64)
	AddPushpullReceivedChanges(count float64)
	AddPushpullSentChanges(count float64)
	ObservePushpullSnapshotDurationSeconds(seconds float64)
	AddPushpullSnapshotBytes(byte float64)
}
