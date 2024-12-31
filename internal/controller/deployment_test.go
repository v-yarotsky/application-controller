package controller

import (
	"context"
	"testing"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	"git.home.yarotsky.me/vlad/application-controller/internal/images"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

func newFakeImageFinder(hasImage bool) *fakeImageFinder {
	return &fakeImageFinder{hasImage: hasImage}
}

type fakeImageFinder struct {
	hasImage bool
}

func (f *fakeImageFinder) FindImage(ctx context.Context, spec yarotskymev1alpha1.ImageSpec) (*images.ImageRef, error) {
	if f.hasImage {
		return &images.ImageRef{
			Repository: "registry.example.com/foo/bar",
			Tag:        "foo",
			Digest:     "sha256:00000000000000000000000000000000",
		}, nil
	} else {
		return nil, images.ErrNotFound
	}
}

func TestDeploymentMutator(t *testing.T) {
	makeMutator := func(app *yarotskymev1alpha1.Application) *deploymentMutator {
		return &deploymentMutator{
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
				Env: []corev1.EnvVar{
					{
						Name:  "MYENVVAR",
						Value: "MYENVVARVALUE",
					},
				},
				EnvFrom: []corev1.EnvFromSource{
					{
						ConfigMapRef: &corev1.ConfigMapEnvSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "myconfigmap",
							},
						},
					},
				},
				Ports: []corev1.ContainerPort{
					{
						Name:          "http",
						ContainerPort: 8080,
						Protocol:      corev1.ProtocolTCP,
					},
				},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						"cpu": resource.MustParse("100m"),
					},
				},
				Probe: &corev1.Probe{
					ProbeHandler: corev1.ProbeHandler{
						HTTPGet: &corev1.HTTPGetAction{
							Path: "/myprobe",
							Port: intstr.FromString("http"),
						},
					},
				},
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
			},
		}
	}

	t.Run(`creates a Deployment using the Application spec`, func(t *testing.T) {
		app := makeApp()

		var deploy appsv1.Deployment
		err := makeMutator(&app).Mutate(context.TODO(), &app, &deploy)()
		assert.NoError(t, err)

		assert.Equal(t, &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"app.kubernetes.io/instance":   "default",
				"app.kubernetes.io/managed-by": "application-controller",
				"app.kubernetes.io/name":       "myapp",
			},
		}, deploy.Spec.Selector)
		assert.Equal(t, appsv1.RecreateDeploymentStrategyType, deploy.Spec.Strategy.Type)

		podSpec := deploy.Spec.Template.Spec
		assert.Equal(t, "myapp", podSpec.ServiceAccountName)
		assert.Len(t, podSpec.Volumes, len(app.Spec.Volumes))
		assert.Equal(t, app.Spec.Volumes[0].Name, podSpec.Volumes[0].Name)
		assert.Equal(t, app.Spec.Volumes[0].Volume.PersistentVolumeClaim.ClaimName, podSpec.Volumes[0].VolumeSource.PersistentVolumeClaim.ClaimName)
		assert.Equal(t, app.Spec.SecurityContext.FSGroup, podSpec.SecurityContext.FSGroup)

		assert.Len(t, podSpec.Containers, 1)
		containerSpec := podSpec.Containers[0]
		assert.Equal(t, app.Name, containerSpec.Name)
		assert.Equal(t, "registry.example.com/foo/bar:foo@sha256:00000000000000000000000000000000", containerSpec.Image)
		assert.Equal(t, corev1.PullAlways, containerSpec.ImagePullPolicy)
		assert.Equal(t, app.Spec.Command, containerSpec.Command)
		assert.Equal(t, app.Spec.Args, containerSpec.Args)
		assert.Equal(t, app.Spec.Env, containerSpec.Env)
		assert.Equal(t, app.Spec.EnvFrom, containerSpec.EnvFrom)
		assert.Equal(t, app.Spec.Ports, containerSpec.Ports)
		assert.Equal(t, app.Spec.Resources, containerSpec.Resources)
		assert.Equal(t, app.Spec.Probe, containerSpec.LivenessProbe)
		assert.Equal(t, app.Spec.Probe, containerSpec.ReadinessProbe)
		assert.Equal(t, app.Spec.SecurityContext.SecurityContext, containerSpec.SecurityContext)

		assert.Len(t, containerSpec.VolumeMounts, len(app.Spec.Volumes))
		assert.Equal(t, app.Spec.Volumes[0].MountPath, containerSpec.VolumeMounts[0].MountPath)
		assert.Equal(t, app.Spec.Volumes[0].Volume.Name, containerSpec.VolumeMounts[0].Name)
	})

	t.Run(`sets pod's hostNetwork to false when hostNetwork is unset`, func(t *testing.T) {
		app := makeApp()

		var deploy appsv1.Deployment
		err := makeMutator(&app).Mutate(context.TODO(), &app, &deploy)()
		assert.NoError(t, err)

		assert.False(t, deploy.Spec.Template.Spec.HostNetwork)
	})

	t.Run(`sets the Pod's DNS policy to "ClusterFirstWithHostNet" when hostNetwork is set`, func(t *testing.T) {
		app := makeApp()
		app.Spec.HostNetwork = true

		var deploy appsv1.Deployment
		err := makeMutator(&app).Mutate(context.TODO(), &app, &deploy)()
		assert.NoError(t, err)

		assert.True(t, deploy.Spec.Template.Spec.HostNetwork)
		assert.Equal(t, corev1.DNSClusterFirstWithHostNet, deploy.Spec.Template.Spec.DNSPolicy)
	})

	t.Run(`does not keep overwriting defaults for Probes`, func(t *testing.T) {
		app := makeApp()
		app.Spec.HostNetwork = true

		var deploy appsv1.Deployment
		err := makeMutator(&app).Mutate(context.TODO(), &app, &deploy)()
		assert.NoError(t, err)

		// simulate a default set by k8s
		deploy.Spec.Template.Spec.Containers[0].LivenessProbe.HTTPGet.Scheme = corev1.URISchemeHTTP

		// re-mutate
		err = makeMutator(&app).Mutate(context.TODO(), &app, &deploy)()
		assert.NoError(t, err)

		assert.Equal(t, corev1.URISchemeHTTP, deploy.Spec.Template.Spec.Containers[0].LivenessProbe.HTTPGet.Scheme)
	})

	t.Run(`sets nodeSelector when specified`, func(t *testing.T) {
		app := makeApp()
		app.Spec.NodeSelector = map[string]string{"foo": "bar"}

		var deploy appsv1.Deployment
		err := makeMutator(&app).Mutate(context.TODO(), &app, &deploy)()
		assert.NoError(t, err)

		assert.Equal(t, map[string]string{"foo": "bar"}, deploy.Spec.Template.Spec.NodeSelector)
	})

	t.Run(`sets runtimeClassName when specified`, func(t *testing.T) {
		app := makeApp()
		app.Spec.RuntimeClassName = ptr.To("foo")

		var deploy appsv1.Deployment
		err := makeMutator(&app).Mutate(context.TODO(), &app, &deploy)()
		assert.NoError(t, err)

		assert.NotNil(t, deploy.Spec.Template.Spec.RuntimeClassName)
		assert.Equal(t, "foo", *deploy.Spec.Template.Spec.RuntimeClassName)
	})
}
