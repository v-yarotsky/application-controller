package controller

import (
	"context"
	"fmt"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

type lbServiceMutator struct {
	namer Namer
}

var (
	ErrUnknownLBServicePort = fmt.Errorf("loadBalancer specifies an unknown port name")
)

func (f *lbServiceMutator) Mutate(ctx context.Context, app *yarotskymev1alpha1.Application, svc *corev1.Service) func() error {
	return func() error {
		lb := app.Spec.LoadBalancer

		appPortsByName := make(map[string]corev1.ContainerPort, len(app.Spec.Ports))
		for _, port := range app.Spec.Ports {
			appPortsByName[port.Name] = port
		}
		ports := make([]corev1.ServicePort, 0, len(lb.PortNames))
		for _, n := range lb.PortNames {
			if p, ok := appPortsByName[n]; !ok {
				return fmt.Errorf("%w: %q", ErrUnknownLBServicePort, n)
			} else {
				ports = append(ports, corev1.ServicePort{
					Name:       p.Name,
					TargetPort: intstr.FromString(p.Name),
					Protocol:   p.Protocol,
					Port:       p.ContainerPort,
				})
			}
		}

		svc.Annotations = addToMap[string, string](svc.Annotations, ExternalDNSHostnameAnnotation, lb.Host)
		svc.Spec.Type = corev1.ServiceTypeLoadBalancer
		svc.Spec.Ports = reconcileServicePorts(svc.Spec.Ports, ports)
		svc.Spec.Selector = f.namer.SelectorLabels()
		return nil
	}
}

func addToMap[K comparable, V any](m map[K]V, key K, value V) map[K]V {
	if m == nil {
		m = make(map[K]V)
	}
	m[key] = value
	return m
}
