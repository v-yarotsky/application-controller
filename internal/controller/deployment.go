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

		deploy.Spec.Template.ObjectMeta = metav1.ObjectMeta{
			Labels: selectorLabels,
		}

		podTemplateSpec := &deploy.Spec.Template.Spec
		podTemplateSpec.ServiceAccountName = f.namer.ServiceAccountName().Name

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
		container.ImagePullPolicy = "Always"
		container.Command = app.Spec.Command
		container.Args = app.Spec.Args
		container.Env = app.Spec.Env
		container.Ports = app.Spec.Ports
		container.Resources = app.Spec.Resources
		container.LivenessProbe = app.Spec.LivenessProbe
		container.ReadinessProbe = app.Spec.ReadinessProbe
		container.StartupProbe = app.Spec.StartupProbe
		container.SecurityContext = app.Spec.SecurityContext

		mounts := make([]corev1.VolumeMount, 0, len(app.Spec.Volumes))
		for _, v := range app.Spec.Volumes {
			mounts = append(mounts, corev1.VolumeMount{
				Name:      v.Name,
				MountPath: v.MountPath,
			})
		}
		container.VolumeMounts = mounts

		return nil
	}
}
