package controller

import (
	"context"
	"fmt"
	"strings"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	traefikv1alpha1 "github.com/traefik/traefik/v2/pkg/provider/kubernetes/crd/traefikio/v1alpha1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

type ingressMutator2 struct {
	namer                     Namer
	DefaultTraefikMiddlewares []string
}

func (f *ingressMutator2) Mutate(ctx context.Context, app *yarotskymev1alpha1.Application, ing *traefikv1alpha1.IngressRoute) func() error {
	return func() error {
		portName := app.Spec.Ingress2.PortName
		if portName == "" {
			for _, p := range app.Spec.Ports {
				if p.Name == "http" {
					portName = "http"
					break
				}
				if p.Name == "web" {
					portName = "web"
					break
				}
			}
		}
		if portName == "" {
			return ErrNoIngressPort
		}
		middlewares := make([]traefikv1alpha1.MiddlewareRef, 0, len(f.DefaultTraefikMiddlewares))
		for _, m := range f.DefaultTraefikMiddlewares {
			var namespace, name string
			parts := strings.SplitN(m, "/", 2)
			if len(parts) == 2 {
				namespace = parts[0]
				name = parts[1]
			} else if len(parts) == 1 {
				namespace = "kube-system"
				name = parts[0]
			}

			middlewares = append(middlewares, traefikv1alpha1.MiddlewareRef{
				Namespace: namespace,
				Name:      name,
			})
		}

		svcName := f.namer.ServiceName()

		ing.Spec.Routes = []traefikv1alpha1.Route{
			{
				Kind:        "Rule",
				Match:       fmt.Sprintf("Host(`%s`)", app.Spec.Ingress2.Host),
				Middlewares: middlewares,
				Services: []traefikv1alpha1.Service{
					{
						LoadBalancerSpec: traefikv1alpha1.LoadBalancerSpec{
							Kind:      "Service",
							Namespace: svcName.Namespace,
							Name:      svcName.Name,
							Port:      intstr.FromString(portName),
						},
					},
				},
			},
		}
		return nil
	}
}
