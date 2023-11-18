package controller

import (
	"context"
	"testing"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	prometheusv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPodMonitorMutator(t *testing.T) {
	makeMutator := func(app *yarotskymev1alpha1.Application) *podMonitorMutator {
		return &podMonitorMutator{
			namer: &simpleNamer{app},
		}
	}

	makeApp := func() yarotskymev1alpha1.Application {
		return yarotskymev1alpha1.Application{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "myapp",
				Namespace: "default",
			},
			Spec: yarotskymev1alpha1.ApplicationSpec{
				Ports: []v1.ContainerPort{
					{
						Name:          "mymetrics",
						ContainerPort: 8080,
					},
				},
				Metrics: &yarotskymev1alpha1.Metrics{
					Enabled:  true,
					PortName: "mymetrics",
					Path:     "/mymetrics",
				},
			},
		}
	}

	t.Run(`creates a PodMonitor with the given metrics port and path`, func(t *testing.T) {
		app := makeApp()

		var mon prometheusv1.PodMonitor
		err := makeMutator(&app).Mutate(context.TODO(), &app, &mon)()
		assert.NoError(t, err)

		assert.Len(t, mon.Spec.PodMetricsEndpoints, 1)
		assert.Equal(t, "mymetrics", mon.Spec.PodMetricsEndpoints[0].Port)
		assert.Equal(t, "/mymetrics", mon.Spec.PodMetricsEndpoints[0].Path)

		assert.Equal(t, map[string]string{
			"app.kubernetes.io/instance":   "default",
			"app.kubernetes.io/managed-by": "application-controller",
			"app.kubernetes.io/name":       "myapp",
		}, mon.Spec.Selector.MatchLabels)
	})

	t.Run(`uses port "metrics" by default`, func(t *testing.T) {
		app := makeApp()
		app.Spec.Ports = append(app.Spec.Ports, corev1.ContainerPort{
			Name:          "metrics",
			ContainerPort: 8081,
		})
		app.Spec.Metrics.PortName = ""

		var mon prometheusv1.PodMonitor
		err := makeMutator(&app).Mutate(context.TODO(), &app, &mon)()
		assert.NoError(t, err)

		assert.Equal(t, "metrics", mon.Spec.PodMetricsEndpoints[0].Port)
	})

	t.Run(`uses port "prometheus" by default`, func(t *testing.T) {
		app := makeApp()
		app.Spec.Ports = append(app.Spec.Ports, corev1.ContainerPort{
			Name:          "prometheus",
			ContainerPort: 8081,
		})
		app.Spec.Metrics.PortName = ""

		var mon prometheusv1.PodMonitor
		err := makeMutator(&app).Mutate(context.TODO(), &app, &mon)()
		assert.NoError(t, err)

		assert.Equal(t, "prometheus", mon.Spec.PodMetricsEndpoints[0].Port)
	})

	t.Run(`fails if no port is specified or inferred`, func(t *testing.T) {
		app := makeApp()
		app.Spec.Metrics.PortName = ""

		var mon prometheusv1.PodMonitor
		err := makeMutator(&app).Mutate(context.TODO(), &app, &mon)()
		assert.ErrorIs(t, err, ErrNoMetricsPort)
	})

	t.Run(`uses path "/metrics" by default`, func(t *testing.T) {
		app := makeApp()
		app.Spec.Metrics.Path = ""

		var mon prometheusv1.PodMonitor
		err := makeMutator(&app).Mutate(context.TODO(), &app, &mon)()
		assert.NoError(t, err)

		assert.Equal(t, "/metrics", mon.Spec.PodMetricsEndpoints[0].Path)
	})
}
