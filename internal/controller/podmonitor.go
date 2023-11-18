package controller

import (
	"context"
	"fmt"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	prometheusv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type podMonitorMutator struct {
	namer Namer
}

var (
	ErrNoMetricsPort = fmt.Errorf(`No monitoring port is specified, and there's no port named "metrics" or "prometheus"`)
)

func (f *podMonitorMutator) Mutate(ctx context.Context, app *yarotskymev1alpha1.Application, mon *prometheusv1.PodMonitor) func() error {
	return func() error {
		portName := app.Spec.Metrics.PortName
		if portName == "" {
			for _, p := range app.Spec.Ports {
				if p.Name == "metrics" || p.Name == "prometheus" {
					portName = p.Name
					break
				}
			}
		}

		if portName == "" {
			return ErrNoMetricsPort
		}

		path := app.Spec.Metrics.Path
		if path == "" {
			path = "/metrics"
		}

		mon.Spec.PodMetricsEndpoints = []prometheusv1.PodMetricsEndpoint{
			{
				Port: portName,
				Path: path,
			},
		}
		mon.Spec.Selector = metav1.LabelSelector{MatchLabels: f.namer.SelectorLabels()}
		mon.Spec.PodTargetLabels = []string{"app.kubernetes.io/name"}
		return nil
	}
}
