package controller

import (
	"context"
	"testing"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	traefikv1alpha1 "github.com/traefik/traefik/v2/pkg/provider/kubernetes/crd/traefikio/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestIngressRouteMutator(t *testing.T) {
	makeMutator := func(app *yarotskymev1alpha1.Application) *ingressRouteMutator {
		return &ingressRouteMutator{
			DefaultTraefikMiddlewares: []string{"https-redirect"},
			namer:                     &simpleNamer{app},
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
						Name:          "myport",
						ContainerPort: 8080,
					},
				},
				Ingress: &yarotskymev1alpha1.Ingress{
					Host:     "myapp.example.com",
					PortName: "myport",
				},
			},
		}
	}

	t.Run(`allows overriding traefik middlewares`, func(t *testing.T) {
		app := makeApp()
		app.Spec.Ingress.TraefikMiddlewares = []string{"https-redirect", "forward-auth"}

		var ing traefikv1alpha1.IngressRoute
		err := makeMutator(&app).Mutate(context.TODO(), &app, &ing)()
		assert.NoError(t, err)

		assert.Len(t, ing.Spec.Routes[0].Middlewares, 2)
		assert.Equal(t, ing.Spec.Routes[0].Middlewares[0].Namespace, "kube-system")
		assert.Equal(t, ing.Spec.Routes[0].Middlewares[0].Name, "https-redirect")
		assert.Equal(t, ing.Spec.Routes[0].Middlewares[1].Namespace, "kube-system")
		assert.Equal(t, ing.Spec.Routes[0].Middlewares[1].Name, "forward-auth")
	})

	t.Run(`uses port "http" by default`, func(t *testing.T) {
		app := makeApp()
		app.Spec.Ports = append(app.Spec.Ports, corev1.ContainerPort{
			Name:          "http",
			ContainerPort: 8080,
		})
		app.Spec.Ingress.PortName = ""

		var ing traefikv1alpha1.IngressRoute
		err := makeMutator(&app).Mutate(context.TODO(), &app, &ing)()
		assert.NoError(t, err)

		assert.Equal(t, "http", ing.Spec.Routes[0].Services[0].Port.StrVal)
	})

	t.Run(`uses port "web" by default`, func(t *testing.T) {
		app := makeApp()
		app.Spec.Ports = append(app.Spec.Ports, corev1.ContainerPort{
			Name:          "web",
			ContainerPort: 8080,
		})
		app.Spec.Ingress.PortName = ""

		var ing traefikv1alpha1.IngressRoute
		err := makeMutator(&app).Mutate(context.TODO(), &app, &ing)()
		assert.NoError(t, err)

		assert.Equal(t, "web", ing.Spec.Routes[0].Services[0].Port.StrVal)
	})

	t.Run(`fails if no port is specified or inferred`, func(t *testing.T) {
		app := makeApp()
		app.Spec.Ingress.PortName = ""

		var ing traefikv1alpha1.IngressRoute
		err := makeMutator(&app).Mutate(context.TODO(), &app, &ing)()
		assert.ErrorIs(t, err, ErrNoIngressRoutePort)
	})

	t.Run(`points at the service`, func(t *testing.T) {
		app := makeApp()

		var ing traefikv1alpha1.IngressRoute
		err := makeMutator(&app).Mutate(context.TODO(), &app, &ing)()
		assert.NoError(t, err)

		assert.Equal(t, app.Namespace, ing.Spec.Routes[0].Services[0].Namespace)
		assert.Equal(t, "myapp", ing.Spec.Routes[0].Services[0].Name)
	})
}
