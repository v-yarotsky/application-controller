package controller

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	traefikv1alpha1 "github.com/traefik/traefik/v2/pkg/provider/kubernetes/crd/traefikio/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
)

var (
	ErrNoIngressRoutePort = fmt.Errorf(`Could not find the port for IngressRoute. Either specify one explicitly, or ensure there's a port named "http" or "web"`)
	ErrBadHost            = fmt.Errorf(`Malformed host for IngressRoute`)
	ErrInvalidAuthConfig  = fmt.Errorf(`Auth configuration is invalid`)
)

var (
	// Ref: https://stackoverflow.com/a/106223
	HostnameRegex = regexp.MustCompile(`^(([a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9\-]*[a-zA-Z0-9])\.)*([A-Za-z0-9]|[A-Za-z0-9][A-Za-z0-9\-]*[A-Za-z0-9])$`)
)

type ingressRouteMutator struct {
	namer                     Namer
	DefaultTraefikMiddlewares []types.NamespacedName
	AuthConfig
}

type AuthConfig struct {
	AuthPathPrefix      string
	AuthServiceName     types.NamespacedName
	AuthServicePortName string
	AuthMiddlewareName  types.NamespacedName
}

func (a AuthConfig) Validate() error {
	if !(strings.HasPrefix(a.AuthPathPrefix, "/") && strings.HasSuffix(a.AuthPathPrefix, "/")) {
		return fmt.Errorf("%w: auth path prefix must begin and end with a `/`", ErrInvalidAuthConfig)
	}
	if a.AuthServiceName.Name == "" {
		return fmt.Errorf("%w: auth service name is not specified", ErrInvalidAuthConfig)
	}
	if a.AuthServicePortName == "" {
		return fmt.Errorf("%w: auth service port name is not specified", ErrInvalidAuthConfig)
	}
	if a.AuthMiddlewareName.Name == "" {
		return fmt.Errorf("%w: auth middleware name is not specified", ErrInvalidAuthConfig)
	}
	return nil
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

		if app.Spec.Ingress.Auth != nil && app.Spec.Ingress.Auth.Enabled {
			if err := f.AuthConfig.Validate(); err != nil {
				return err
			}

			ing.Spec.Routes = []traefikv1alpha1.Route{
				{
					Kind:        "Rule",
					Match:       fmt.Sprintf("Host(`%s`) && PathPrefix(`%s`)", app.Spec.Ingress.Host, f.AuthPathPrefix),
					Middlewares: f.middlewares(f.DefaultTraefikMiddlewares),
					Services: []traefikv1alpha1.Service{
						{
							LoadBalancerSpec: traefikv1alpha1.LoadBalancerSpec{
								Kind:      "Service",
								Namespace: f.AuthServiceName.Namespace,
								Name:      f.AuthServiceName.Name,
								Port:      intstr.FromString(f.AuthServicePortName),
							},
						},
					},
				},
				{
					Kind:        "Rule",
					Match:       fmt.Sprintf("Host(`%s`)", app.Spec.Ingress.Host),
					Middlewares: f.middlewares(append(f.DefaultTraefikMiddlewares, f.AuthMiddlewareName)),
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
		} else {
			ing.Spec.Routes = []traefikv1alpha1.Route{
				{
					Kind:        "Rule",
					Match:       fmt.Sprintf("Host(`%s`)", app.Spec.Ingress.Host),
					Middlewares: f.middlewares(f.DefaultTraefikMiddlewares),
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
		}
		return nil
	}
}

func (f *ingressRouteMutator) middlewares(middlewares []types.NamespacedName) []traefikv1alpha1.MiddlewareRef {
	result := make([]traefikv1alpha1.MiddlewareRef, 0, len(middlewares))
	for _, m := range middlewares {
		result = append(result, traefikv1alpha1.MiddlewareRef{
			Namespace: m.Namespace,
			Name:      m.Name,
		})
	}
	return result
}
