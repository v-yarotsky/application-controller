package controller

import (
	"context"
	"fmt"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/utils/ptr"
)

type ingressMutator struct {
	DefaultIngressClassName   string
	DefaultIngressAnnotations map[string]string
	namer                     Namer
}

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

		ing.ObjectMeta.Annotations = f.DefaultIngressAnnotations

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
