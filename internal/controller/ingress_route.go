package controller

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	traefikv1alpha1 "github.com/traefik/traefik/v2/pkg/provider/kubernetes/crd/traefikio/v1alpha1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

var (
	ErrNoIngressRoutePort = fmt.Errorf(`Could not find the port for IngressRoute. Either specify one explicitly, or ensure there's a port named "http" or "web"`)
	ErrBadHost            = fmt.Errorf(`Malformed host for IngressRoute`)
)

var (
	// Ref: https://stackoverflow.com/a/106223
	HostnameRegex = regexp.MustCompile(`^(([a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9\-]*[a-zA-Z0-9])\.)*([A-Za-z0-9]|[A-Za-z0-9][A-Za-z0-9\-]*[A-Za-z0-9])$`)
)

type ingressRouteMutator struct {
	namer                     Namer
	DefaultTraefikMiddlewares []string
}

func (f *ingressRouteMutator) Mutate(ctx context.Context, app *yarotskymev1alpha1.Application, ing *traefikv1alpha1.IngressRoute) func() error {
	return func() error {
		hostname := app.Spec.Ingress.Host

		if !HostnameRegex.MatchString(hostname) {
			return ErrBadHost
		}

		portName := app.Spec.Ingress.PortName
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
			return ErrNoIngressRoutePort
		}

		svcName := f.namer.ServiceName()

		ing.Spec.Routes = []traefikv1alpha1.Route{
			{
				Kind:        "Rule",
				Match:       fmt.Sprintf("Host(`%s`)", app.Spec.Ingress.Host),
				Middlewares: f.middlewares(app, ing),
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

func (f *ingressRouteMutator) middlewares(app *yarotskymev1alpha1.Application, ing *traefikv1alpha1.IngressRoute) []traefikv1alpha1.MiddlewareRef {
	var ms []string
	if len(app.Spec.Ingress.TraefikMiddlewares) > 0 {
		ms = app.Spec.Ingress.TraefikMiddlewares
	} else {
		ms = f.DefaultTraefikMiddlewares
	}

	middlewares := make([]traefikv1alpha1.MiddlewareRef, 0, len(ms))
	for _, m := range ms {
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
	return middlewares
}
