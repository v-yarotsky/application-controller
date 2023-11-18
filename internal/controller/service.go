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

		svc.Spec.Ports = reconcilePorts(svc.Spec.Ports, ports)
		svc.Spec.Selector = f.namer.SelectorLabels()
		return nil
	}
}
