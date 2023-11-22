package controller

import (
	"context"
	"errors"
	"fmt"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	"git.home.yarotsky.me/vlad/application-controller/internal/images"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type deploymentMutator struct {
	namer       Namer
	imageFinder images.ImageFinder
}

var ErrImageCheckFailed = errors.New("Failed to check for new versions of the image")

func (f *deploymentMutator) Mutate(ctx context.Context, app *yarotskymev1alpha1.Application, deploy *appsv1.Deployment) func() error {
	return func() error {
		imgRef, err := f.imageFinder.FindImage(ctx, app.Spec.Image)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrImageCheckFailed, err)
		}
		selectorLabels := f.namer.SelectorLabels()

		deploy.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: selectorLabels,
		}

		deploy.Spec.Strategy.Type = appsv1.RecreateDeploymentStrategyType

		deploy.Spec.Template.ObjectMeta = metav1.ObjectMeta{
			Labels: selectorLabels,
		}

		podTemplateSpec := &deploy.Spec.Template.Spec
		podTemplateSpec.ServiceAccountName = f.namer.ServiceAccountName().Name

		if app.Spec.HostNetwork {
			podTemplateSpec.HostNetwork = true

			// To have DNS options set along with hostNetwork, you have to specify DNS policy
			// explicitly to 'ClusterFirstWithHostNet'.
			podTemplateSpec.DNSPolicy = corev1.DNSClusterFirstWithHostNet
		}

		vols := make([]corev1.Volume, 0, len(app.Spec.Volumes))
		for _, v := range app.Spec.Volumes {
			vols = append(vols, v.Volume)
		}
		podTemplateSpec.Volumes = vols

		var container *corev1.Container
		if len(podTemplateSpec.Containers) == 0 {
			podTemplateSpec.Containers = []corev1.Container{{}}
		}
		container = &podTemplateSpec.Containers[0]

		container.Name = app.Name
		container.Image = imgRef.String()
		container.ImagePullPolicy = corev1.PullAlways
		container.Command = app.Spec.Command
		container.Args = app.Spec.Args
		container.Env = app.Spec.Env
		container.EnvFrom = app.Spec.EnvFrom
		container.Ports = reconcileContainerPorts(container.Ports, app.Spec.Ports)
		container.Resources = app.Spec.Resources
		container.LivenessProbe = app.Spec.Probe
		container.ReadinessProbe = app.Spec.Probe

		mounts := make([]corev1.VolumeMount, 0, len(app.Spec.Volumes))
		for _, v := range app.Spec.Volumes {
			mounts = append(mounts, corev1.VolumeMount{
				Name:      v.Name,
				MountPath: v.MountPath,
			})
		}
		container.VolumeMounts = mounts

		if sc := app.Spec.SecurityContext; sc != nil {
			container.SecurityContext = sc.SecurityContext

			podTemplateSpec.SecurityContext = &corev1.PodSecurityContext{
				SupplementalGroups:  sc.SupplementalGroups,
				FSGroup:             sc.FSGroup,
				Sysctls:             sc.Sysctls,
				FSGroupChangePolicy: sc.FSGroupChangePolicy,
			}
		}

		return nil
	}
}

func reconcileContainerPorts(actual []corev1.ContainerPort, desired []corev1.ContainerPort) []corev1.ContainerPort {
	if len(actual) == 0 {
		return desired
	}

	desiredPortsByName := make(map[string]corev1.ContainerPort, len(desired))
	seen := make(map[string]bool, len(desired))

	for _, p := range desired {
		desiredPortsByName[p.Name] = p
	}

	result := make([]corev1.ContainerPort, 0, len(desired))
	for _, got := range actual {
		if want, ok := desiredPortsByName[got.Name]; ok {
			got.ContainerPort = want.ContainerPort
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
