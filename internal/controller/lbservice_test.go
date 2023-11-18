package controller

import (
	"context"
	"testing"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestLBServiceMutator(t *testing.T) {
	makeMutator := func(app *yarotskymev1alpha1.Application) *lbServiceMutator {
		return &lbServiceMutator{
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
						Name:          "myport1",
						ContainerPort: 8080,
					},
					{
						Name:          "myport2",
						ContainerPort: 8081,
					},
				},
				LoadBalancer: &yarotskymev1alpha1.LoadBalancer{
					Host:      "myapp.example.com",
					PortNames: []string{"myport1", "myport2"},
				},
			},
		}
	}

	t.Run(`creates a LoadBalancer Service with the given ports and a hostname annotation`, func(t *testing.T) {
		app := makeApp()

		var svc corev1.Service
		err := makeMutator(&app).Mutate(context.TODO(), &app, &svc)()
		assert.NoError(t, err)

		assert.Contains(t, svc.Annotations, ExternalDNSHostnameAnnotation)
		assert.Equal(t, svc.Annotations[ExternalDNSHostnameAnnotation], "myapp.example.com")
		assert.Equal(t, corev1.ServiceTypeLoadBalancer, svc.Spec.Type)
		assert.Equal(t, map[string]string{
			"app.kubernetes.io/instance":   "default",
			"app.kubernetes.io/managed-by": "application-controller",
			"app.kubernetes.io/name":       "myapp",
		}, svc.Spec.Selector)
		assert.Equal(t, "myport1", svc.Spec.Ports[0].TargetPort.StrVal)
		assert.Equal(t, "myport2", svc.Spec.Ports[1].TargetPort.StrVal)
	})

	t.Run(`fails if an unknown port is specified`, func(t *testing.T) {
		app := makeApp()
		app.Spec.LoadBalancer.PortNames = []string{"wat"}

		var svc corev1.Service
		err := makeMutator(&app).Mutate(context.TODO(), &app, &svc)()
		assert.ErrorIs(t, err, ErrUnknownLBServicePort)
	})
}
