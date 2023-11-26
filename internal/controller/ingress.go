package controller

import (
	"context"
	"fmt"
	"strings"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/utils/ptr"
)

type ingressMutator struct {
	DefaultIngressClassName   string
	DefaultTraefikMiddlewares []string
	namer                     Namer
}

const (
	traefikMiddlewaresIngressAnnotation = "traefik.ingress.kubernetes.io/router.middlewares"
)

var (
	ErrNoIngressPort  = fmt.Errorf(`Could not find the port for Ingress. Either specify one explicitly, or ensure there's a port named "http" or "web"`)
	ErrNoIngressClass = fmt.Errorf(`ingress.ingressClassName is not specified, and --ingress-class is not set.`)
)

func (f *ingressMutator) Mutate(ctx context.Context, app *yarotskymev1alpha1.Application, ing *networkingv1.Ingress) func() error {
	return func() error {
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
			return ErrNoIngressPort
		}
		pathType := networkingv1.PathTypeImplementationSpecific

		ingressClassName := app.Spec.Ingress.IngressClassName
		if ingressClassName == nil {
			if f.DefaultIngressClassName == "" {
				return ErrNoIngressClass
			}
			ingressClassName = ptr.To(f.DefaultIngressClassName)
		}

		var middlewares []string
		if ms := app.Spec.Ingress.TraefikMiddlewares; len(ms) > 0 {
			middlewares = ms
		} else if ms := f.DefaultTraefikMiddlewares; len(ms) > 0 {
			middlewares = ms
		}

		if len(middlewares) > 0 {
			if ing.ObjectMeta.Annotations == nil {
				ing.ObjectMeta.Annotations = map[string]string{}
			}
			ing.ObjectMeta.Annotations[traefikMiddlewaresIngressAnnotation] = traefikMiddlewaresAnnotationValue(middlewares)
		} else {
			delete(ing.ObjectMeta.Annotations, traefikMiddlewaresIngressAnnotation)
		}

		ing.Spec.IngressClassName = ingressClassName
		ing.Spec.Rules = []networkingv1.IngressRule{
			{
				Host: app.Spec.Ingress.Host,
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{
							{
								Path:     "/",
								PathType: &pathType,
								Backend: networkingv1.IngressBackend{
									Service: &networkingv1.IngressServiceBackend{
										Name: f.namer.ServiceName().Name,
										Port: networkingv1.ServiceBackendPort{
											Name: portName,
										},
									},
								},
							},
						},
					},
				},
			},
		}
		return nil
	}
}

func traefikMiddlewaresAnnotationValue(middlewares []string) string {
	tmp := make([]string, len(middlewares))
	for i, m := range middlewares {
		tmp[i] = fmt.Sprintf("kube-system-%s@kubernetescrd", m)
	}
	return strings.Join(tmp, ",")
}
