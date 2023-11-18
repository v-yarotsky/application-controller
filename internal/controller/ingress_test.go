package controller

import (
	"context"
	"testing"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestIngressMutator(t *testing.T) {
	makeMutator := func(app *yarotskymev1alpha1.Application) *ingressMutator {
		return &ingressMutator{
			DefaultIngressClassName: "myingressclass",
			DefaultIngressAnnotations: map[string]string{
				"my.ingress/annotation": "my-annotation-value",
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

	t.Run(`uses given default ingressClassName and annotations`, func(t *testing.T) {
		app := makeApp()

		var ing networkingv1.Ingress
		err := makeMutator(&app).Mutate(context.TODO(), &app, &ing)()
		assert.NoError(t, err)

		assert.Equal(t, "myingressclass", *ing.Spec.IngressClassName)
		assert.Equal(t, map[string]string{"my.ingress/annotation": "my-annotation-value"}, ing.Annotations)
	})

	t.Run(`allows overriding ingressClassName`, func(t *testing.T) {
		app := makeApp()
		app.Spec.Ingress.IngressClassName = ptr.To("myotheringressclass")

		var ing networkingv1.Ingress
		err := makeMutator(&app).Mutate(context.TODO(), &app, &ing)()
		assert.NoError(t, err)

		assert.Equal(t, "myotheringressclass", *ing.Spec.IngressClassName)
	})

	t.Run(`fails without default ingress class when none provided explicitly`, func(t *testing.T) {
		app := makeApp()

		var ing networkingv1.Ingress
		mutator := makeMutator(&app)
		mutator.DefaultIngressClassName = ""

		err := mutator.Mutate(context.TODO(), &app, &ing)()
		assert.ErrorIs(t, err, ErrNoIngressClass)
	})

	t.Run(`uses port "http" by default`, func(t *testing.T) {
		app := makeApp()
		app.Spec.Ports = append(app.Spec.Ports, corev1.ContainerPort{
			Name:          "http",
			ContainerPort: 8080,
		})
		app.Spec.Ingress.PortName = ""

		var ing networkingv1.Ingress
		err := makeMutator(&app).Mutate(context.TODO(), &app, &ing)()
		assert.NoError(t, err)

		assert.Equal(t, "http", ing.Spec.Rules[0].IngressRuleValue.HTTP.Paths[0].Backend.Service.Port.Name)
	})

	t.Run(`uses port "web" by default`, func(t *testing.T) {
		app := makeApp()
		app.Spec.Ports = append(app.Spec.Ports, corev1.ContainerPort{
			Name:          "web",
			ContainerPort: 8080,
		})
		app.Spec.Ingress.PortName = ""

		var ing networkingv1.Ingress
		err := makeMutator(&app).Mutate(context.TODO(), &app, &ing)()
		assert.NoError(t, err)

		assert.Equal(t, "web", ing.Spec.Rules[0].IngressRuleValue.HTTP.Paths[0].Backend.Service.Port.Name)
	})

	t.Run(`fails if no port is specified or inferred`, func(t *testing.T) {
		app := makeApp()
		app.Spec.Ingress.PortName = ""

		var ing networkingv1.Ingress
		err := makeMutator(&app).Mutate(context.TODO(), &app, &ing)()
		assert.ErrorIs(t, err, ErrNoIngressPort)
	})

	t.Run(`points at the service`, func(t *testing.T) {
		app := makeApp()

		var ing networkingv1.Ingress
		err := makeMutator(&app).Mutate(context.TODO(), &app, &ing)()
		assert.NoError(t, err)

		assert.Equal(t, "myapp", ing.Spec.Rules[0].IngressRuleValue.HTTP.Paths[0].Backend.Service.Name)
	})
}
