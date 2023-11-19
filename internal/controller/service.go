package controller

import (
	"context"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

type serviceMutator struct {
	namer Namer
}

func (f *serviceMutator) Mutate(ctx context.Context, app *yarotskymev1alpha1.Application, svc *corev1.Service) func() error {
	return func() error {
		ports := make([]corev1.ServicePort, 0, len(app.Spec.Ports))
		for _, p := range app.Spec.Ports {
			ports = append(ports, corev1.ServicePort{
				Name:       p.Name,
				TargetPort: intstr.FromString(p.Name),
				Protocol:   p.Protocol,
				Port:       p.ContainerPort,
			})
		}

		svc.Spec.Ports = reconcileServicePorts(svc.Spec.Ports, ports)
		svc.Spec.Selector = f.namer.SelectorLabels()
		return nil
	}
}

func reconcileServicePorts(actual []corev1.ServicePort, desired []corev1.ServicePort) []corev1.ServicePort {
	if len(actual) == 0 {
		return desired
	}

	desiredPortsByName := make(map[string]corev1.ServicePort, len(desired))
	seen := make(map[string]bool, len(desired))

	for _, p := range desired {
		desiredPortsByName[p.Name] = p
	}

	result := make([]corev1.ServicePort, 0, len(desired))
	for _, got := range actual {
		if want, ok := desiredPortsByName[got.Name]; ok {
			got.TargetPort = want.TargetPort
			got.Protocol = want.Protocol
			result = append(result, got)
			seen[got.Name] = true
		}
	}

	for _, want := range desired {
		if seen[want.Name] {
			continue
		}
		result = append(result, want)
	}

	return result
}
