package images

import (
	"context"
	"fmt"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	"git.home.yarotsky.me/vlad/application-controller/internal/k8s"
	"github.com/google/go-containerregistry/pkg/authn"
	kubeauth "github.com/google/go-containerregistry/pkg/authn/kubernetes"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

type ImageFinder interface {
	// FindImage takes an image version tracking spec, and returns
	// a new image reference.
	FindImage(ctx context.Context, spec yarotskymev1alpha1.ImageSpec) (string, error)
}

func NewImageFinder(opts ...Option) (*imageFinder, error) {
	cfg := &config{}
	for _, opt := range opts {
		err := opt(cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to apply option: %w", err)
		}
	}
	return &imageFinder{cfg}, nil
}

type Option func(*config) error

func WithInClusterRegistryAuth(imagePullSecrets []string) Option {
	return func(opts *config) error {
		namespace, err := k8s.CurrentNamespace()
		if err != nil {
			return fmt.Errorf("failed to read current namespace: %w", err)
		}

		// If cloud auth is needed, use github.com/google/go-containerregistry/pkg/authn/k8schain instead
		kc, err := kubeauth.NewInCluster(context.Background(), kubeauth.Options{
			Namespace:          namespace,
			ServiceAccountName: kubeauth.NoServiceAccount,
			ImagePullSecrets:   imagePullSecrets,
		})
		if err != nil {
			return fmt.Errorf("failed to obtain image registry authn keychain: %w", err)
		}
		opts.keychain = kc
		return nil
	}
}

type imageFinder struct {
	*config
}

type config struct {
	keychain authn.Keychain
}

type ImageRef struct {
	Repository string
	Tag        string
	Digest     string
}

func (r *ImageRef) String() string {
	if r.Tag != "" && r.Digest != "" {
		return fmt.Sprintf("%s:%s@%s", r.Repository, r.Tag, r.Digest)
	} else if r.Digest == "" {
		return fmt.Sprintf("%s:%s", r.Repository, r.Tag)
	} else if r.Tag == "" {
		return fmt.Sprintf("%s@%s", r.Repository, r.Digest)
	}
	return "<invalid image reference>"
}

func (r *ImageRef) ToGoContainerRegistryReference() name.Reference {
	ref, _ := name.ParseReference(r.String())
	return ref
}

func (f *imageFinder) FindImage(ctx context.Context, spec yarotskymev1alpha1.ImageSpec) (string, error) {
	// TODO: handle other version strategies besides `digest`
	tagRef := ImageRef{Repository: spec.Repository, Tag: spec.Digest.Tag}

	descriptor, err := remote.Get(tagRef.ToGoContainerRegistryReference(), remote.WithContext(ctx), remote.WithAuthFromKeychain(f.keychain))
	if err != nil {
		return "", fmt.Errorf("failed to retrieve digest for %s: %w", tagRef.String(), err)
	}

	newRef := tagRef
	newRef.Digest = descriptor.Digest.String()
	return newRef.String(), nil
}
