package controller

import (
	"context"
	"testing"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	"git.home.yarotsky.me/vlad/application-controller/internal/images"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
				SecurityContext: &yarotskymev1alpha1.SecurityContext{
					SecurityContext: &corev1.SecurityContext{
						RunAsUser: ptr.To(int64(1000)),
					},
					FSGroup: ptr.To(int64(1000)),
				},
			},
		}
	}

	t.Run(`sets SecurityContext settings on the Pod and the Container`, func(t *testing.T) {
		app := makeApp()

		var deploy appsv1.Deployment
		err := makeMutator(&app).Mutate(context.TODO(), &app, &deploy)()
		assert.NoError(t, err)

		assert.Equal(t, int64(1000), *deploy.Spec.Template.Spec.SecurityContext.FSGroup)
		assert.Equal(t, int64(1000), *deploy.Spec.Template.Spec.Containers[0].SecurityContext.RunAsUser)
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
}
