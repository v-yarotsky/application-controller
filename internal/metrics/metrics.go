package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	rmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	ImageRegistryCalls = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "image_registry_calls_total",
			Help: "Number image registry calls",
		},
		[]string{"registry", "repository", "method", "success"},
	)
)

func init() {
	rmetrics.Registry.MustRegister(ImageRegistryCalls)
}
