package controller

import (
	"context"
	"fmt"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	"git.home.yarotsky.me/vlad/application-controller/internal/images"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

type cronJobMutator struct {
	namer       Namer
	imageFinder images.ImageFinder
}

func (f *cronJobMutator) Mutate(ctx context.Context, app *yarotskymev1alpha1.Application, cronJob *batchv1.CronJob, spec yarotskymev1alpha1.CronJobSpec) func() error {
	return func() error {
		imgRef, err := f.imageFinder.FindImage(ctx, app.Spec.Image)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrImageCheckFailed, err)
		}

		selectorLabels := f.namer.CronJobSelectorLabels(spec.Name)

		cronJob.Spec.JobTemplate.ObjectMeta = metav1.ObjectMeta{
			Labels: selectorLabels,
		}

		cronJob.Spec.ConcurrencyPolicy = batchv1.ForbidConcurrent
		cronJob.Spec.Schedule = spec.Schedule
		cronJob.Spec.SuccessfulJobsHistoryLimit = ptr.To[int32](3)
		cronJob.Spec.FailedJobsHistoryLimit = ptr.To[int32](3)

		podTemplateSpec := &cronJob.Spec.JobTemplate.Spec.Template.Spec
		podTemplateSpec.ServiceAccountName = f.namer.ServiceAccountName().Name
		podTemplateSpec.NodeSelector = app.Spec.NodeSelector
		podTemplateSpec.RestartPolicy = corev1.RestartPolicyOnFailure

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
		container.Command = spec.Command
		container.Args = spec.Args
		container.Env = app.Spec.Env
		container.EnvFrom = app.Spec.EnvFrom
		container.Resources = spec.Resources

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

		podTemplateSpec.RuntimeClassName = app.Spec.RuntimeClassName

		return nil
	}
}
