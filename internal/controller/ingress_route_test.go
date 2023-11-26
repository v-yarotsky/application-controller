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
	"k8s.io/apimachinery/pkg/types"
)

func TestIngressRouteMutator(t *testing.T) {
	makeMutator := func(app *yarotskymev1alpha1.Application) *ingressRouteMutator {
		return &ingressRouteMutator{
			DefaultTraefikMiddlewares: []types.NamespacedName{{Namespace: "kube-system", Name: "https-redirect"}},
			AuthConfig: AuthConfig{
				AuthPathPrefix: "/oauth2/",
				AuthServiceName: types.NamespacedName{
					Namespace: "kube-system",
					Name:      "oauth2-proxy",
				},
				AuthServicePortName: "http",
				AuthMiddlewareName: types.NamespacedName{
					Namespace: "kube-system",
					Name:      "oauth2",
				},
			},
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

	t.Run(`populates default middleware`, func(t *testing.T) {
		app := makeApp()

		var ing traefikv1alpha1.IngressRoute
		err := makeMutator(&app).Mutate(context.TODO(), &app, &ing)()
		assert.NoError(t, err)

		assert.Len(t, ing.Spec.Routes[0].Middlewares, 1)
		assert.Equal(t, "kube-system", ing.Spec.Routes[0].Middlewares[0].Namespace)
		assert.Equal(t, "https-redirect", ing.Spec.Routes[0].Middlewares[0].Name)
	})

	t.Run(`adds the auth service route when auth proxy is enabled`, func(t *testing.T) {
		app := makeApp()
		app.Spec.Ingress.Auth = &yarotskymev1alpha1.IngressAuth{
			Enabled: true,
		}

		var ing traefikv1alpha1.IngressRoute
		err := makeMutator(&app).Mutate(context.TODO(), &app, &ing)()
		assert.NoError(t, err)

		assert.Len(t, ing.Spec.Routes, 2)

		r1 := ing.Spec.Routes[0]
		assert.Equal(t, "Host(`myapp.example.com`) && PathPrefix(`/oauth2/`)", r1.Match)
		assert.Equal(t, "Service", r1.Services[0].Kind)
		assert.Equal(t, "oauth2-proxy", r1.Services[0].Name)
		assert.Equal(t, "kube-system", r1.Services[0].Namespace)
		assert.Equal(t, "http", r1.Services[0].Port.StrVal)

		assert.Len(t, r1.Middlewares, 1)
		assert.Equal(t, "https-redirect", r1.Middlewares[0].Name)

		r2 := ing.Spec.Routes[1]
		assert.Equal(t, "Host(`myapp.example.com`)", r2.Match)
		assert.Equal(t, "Service", r2.Services[0].Kind)
		assert.Equal(t, "myapp", r2.Services[0].Name)
		assert.Equal(t, "default", r2.Services[0].Namespace)
		assert.Equal(t, "myport", r2.Services[0].Port.StrVal)

		assert.Len(t, r2.Middlewares, 2)
		assert.Equal(t, "https-redirect", r2.Middlewares[0].Name)
		assert.Equal(t, "oauth2", r2.Middlewares[1].Name)
	})

	t.Run(`fails if auth is not configured properly`, func(t *testing.T) {
		app := makeApp()
		app.Spec.Ingress.Auth = &yarotskymev1alpha1.IngressAuth{
			Enabled: true,
		}

		var ing traefikv1alpha1.IngressRoute
		mut := makeMutator(&app)
		mut.AuthPathPrefix = ""
		err := mut.Mutate(context.TODO(), &app, &ing)()
		assert.ErrorIs(t, err, ErrInvalidAuthConfig)
	})
}
