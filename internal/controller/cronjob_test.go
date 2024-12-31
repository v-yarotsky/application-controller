package controller

import (
	"context"
	"testing"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestCronJobMutator(t *testing.T) {
	makeMutator := func(app *yarotskymev1alpha1.Application) *cronJobMutator {
		return &cronJobMutator{
			namer:       &simpleNamer{app},
			imageFinder: newFakeImageFinder(true),
		}
	}

	makeApp := func() yarotskymev1alpha1.Application {
		return yarotskymev1alpha1.Application{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "myapp",
				Namespace: "default",
			},
			Spec: yarotskymev1alpha1.ApplicationSpec{
				Image: yarotskymev1alpha1.ImageSpec{
					Repository: "registry.example.com/foo/bar",
				},
				Command: []string{"/mycommand"},
				Args:    []string{"myarg"},
				SecurityContext: &yarotskymev1alpha1.SecurityContext{
					SecurityContext: &corev1.SecurityContext{
						RunAsUser: ptr.To(int64(1000)),
					},
					FSGroup: ptr.To(int64(1000)),
				},
				Volumes: []yarotskymev1alpha1.Volume{
					{
						Volume: corev1.Volume{
							Name: "myvolume",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: "mypvc",
								},
							},
						},
						MountPath: "/myvolume",
					},
				},
				CronJobs: []yarotskymev1alpha1.CronJobSpec{
					{
						Name:     "mycronjob",
						Schedule: "@daily",
						Command:  []string{"/mycronjobcommand"},
						Args:     []string{"mycronjobarg"},
					},
					{
						Name:     "myhourlycronjob",
						Schedule: "@hourly",
						Command:  []string{"/myhourlycronjobcommand"},
						Args:     []string{"myhourlycronjobarg"},
					},
				},
			},
		}
	}

	t.Run(`creates a CronJob using the Application spec`, func(t *testing.T) {
		app := makeApp()

		var cronJob batchv1.CronJob
		err := makeMutator(&app).Mutate(context.TODO(), &app, &cronJob, app.Spec.CronJobs[0])()
		assert.NoError(t, err)

		assert.Equal(t, map[string]string{
			"app.kubernetes.io/instance":   "default",
			"app.kubernetes.io/managed-by": "application-controller",
			"app.kubernetes.io/part-of":    "myapp",
			"app.kubernetes.io/name":       "mycronjob",
		}, cronJob.Spec.JobTemplate.ObjectMeta.Labels)
		assert.Equal(t, batchv1.ForbidConcurrent, cronJob.Spec.ConcurrencyPolicy)
		assert.Equal(t, "@daily", cronJob.Spec.Schedule)
		assert.Equal(t, int32(3), *cronJob.Spec.SuccessfulJobsHistoryLimit)
		assert.Equal(t, int32(3), *cronJob.Spec.FailedJobsHistoryLimit)

		podSpec := cronJob.Spec.JobTemplate.Spec.Template.Spec
		assert.Equal(t, "myapp", podSpec.ServiceAccountName)
		assert.Len(t, podSpec.Volumes, len(app.Spec.Volumes))
		assert.Equal(t, app.Spec.Volumes[0].Name, podSpec.Volumes[0].Name)
		assert.Equal(t, app.Spec.Volumes[0].Volume.PersistentVolumeClaim.ClaimName, podSpec.Volumes[0].VolumeSource.PersistentVolumeClaim.ClaimName)

		assert.Len(t, podSpec.Containers, 1)
		containerSpec := podSpec.Containers[0]
		assert.Equal(t, app.Name, containerSpec.Name)
		assert.Equal(t, "registry.example.com/foo/bar:foo@sha256:00000000000000000000000000000000", containerSpec.Image)
		assert.Equal(t, corev1.PullAlways, containerSpec.ImagePullPolicy)
		assert.Equal(t, app.Spec.CronJobs[0].Command, containerSpec.Command)
		assert.Equal(t, app.Spec.CronJobs[0].Args, containerSpec.Args)
		assert.Equal(t, app.Spec.CronJobs[0].Resources, containerSpec.Resources)
		assert.Equal(t, app.Spec.Env, containerSpec.Env)
		assert.Equal(t, app.Spec.EnvFrom, containerSpec.EnvFrom)
		assert.Equal(t, app.Spec.SecurityContext.SecurityContext, containerSpec.SecurityContext)

		assert.Len(t, containerSpec.VolumeMounts, len(app.Spec.Volumes))
		assert.Equal(t, app.Spec.Volumes[0].MountPath, containerSpec.VolumeMounts[0].MountPath)
		assert.Equal(t, app.Spec.Volumes[0].Volume.Name, containerSpec.VolumeMounts[0].Name)
	})

	t.Run(`sets runtimeClassName when specified`, func(t *testing.T) {
		app := makeApp()
		app.Spec.RuntimeClassName = ptr.To("foo")

		var cronJob batchv1.CronJob
		err := makeMutator(&app).Mutate(context.TODO(), &app, &cronJob, app.Spec.CronJobs[0])()
		assert.NoError(t, err)

		assert.NotNil(t, cronJob.Spec.JobTemplate.Spec.Template.Spec.RuntimeClassName)
		assert.Equal(t, "foo", *cronJob.Spec.JobTemplate.Spec.Template.Spec.RuntimeClassName)
	})
}
