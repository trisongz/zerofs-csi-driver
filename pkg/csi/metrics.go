package csi

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	nodeOpDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "zerofs_csi_node_operation_seconds",
			Help:    "Duration of node-side CSI operations by stage.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"operation", "stage"},
	)

	nodeOpErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "zerofs_csi_node_errors_total",
			Help: "Count of node-side CSI errors by stage.",
		},
		[]string{"operation", "stage"},
	)
)

func init() {
	metrics.Registry.MustRegister(nodeOpDuration, nodeOpErrors)
}

type stageTimer struct {
	operation string
	stage     string
	start     time.Time
}

func startStage(operation, stage string) stageTimer {
	return stageTimer{operation: operation, stage: stage, start: time.Now()}
}

func (t stageTimer) observe() {
	nodeOpDuration.WithLabelValues(t.operation, t.stage).Observe(time.Since(t.start).Seconds())
}

func recordStageError(operation, stage string) {
	nodeOpErrors.WithLabelValues(operation, stage).Inc()
}
