package images

import (
	"context"
	"errors"
	"fmt"
	"sort"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	"git.home.yarotsky.me/vlad/application-controller/internal/k8s"
	"github.com/Masterminds/semver/v3"
	kubeauth "github.com/google/go-containerregistry/pkg/authn/kubernetes"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

type ImageFinder interface {
	// FindImage takes an image version tracking spec, and returns
	// a new image reference.
	FindImage(ctx context.Context, spec yarotskymev1alpha1.ImageSpec) (*ImageRef, error)
}

func NewImageFinder(opts ...Option) (*imageFinder, error) {
	cfg := &config{
		registryClient: &simpleRegistryClient{},
	}
	for _, opt := range opts {
		err := opt(cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to apply option: %w", err)
		}
	}
	return &imageFinder{cfg}, nil
}

type Option func(*config) error

func WithAuthenticatedRegistryClient(imagePullSecrets []string) Option {
	return func(c *config) error {
		client := &simpleRegistryClient{}
		if len(imagePullSecrets) == 0 {
			c.registryClient = client
			return nil
		}

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
		client.extraOpts = []remote.Option{remote.WithAuthFromKeychain(kc)}
		return nil
	}
}

func WithRegistryClient(t RegistryClient) Option {
	return func(c *config) error {
		c.registryClient = t
		return nil
	}
}

type imageFinder struct {
	*config
}

var _ ImageFinder = &imageFinder{}

type config struct {
	registryClient RegistryClient
}

func (f *imageFinder) FindImage(ctx context.Context, spec yarotskymev1alpha1.ImageSpec) (*ImageRef, error) {
	switch spec.VersionStrategy {
	case yarotskymev1alpha1.VersionStrategyDigest:
		return f.findImageByDigest(ctx, spec.Repository, spec.Digest)
	case yarotskymev1alpha1.VersionStrategySemver:
		return f.findImageBySemver(ctx, spec.Repository, spec.SemVer)
	default:
		return nil, fmt.Errorf("`%s` is not a supported versionStrategy.", spec.VersionStrategy)
	}
}

func (f *imageFinder) findImageByDigest(ctx context.Context, repo string, spec *yarotskymev1alpha1.VersionStrategyDigestSpec) (*ImageRef, error) {
	if spec == nil {
		return nil, fmt.Errorf("`digest` version strategy spec is empty!")
	}

	tagRef := ImageRef{Repository: repo, Tag: spec.Tag}
	digest, err := f.registryClient.GetDigest(ctx, tagRef)
	if err != nil {
		return nil, err
	}

	newRef := tagRef
	newRef.Digest = digest
	return &newRef, nil
}

var (
	ErrNotFound = errors.New("Suitable image not found")
)

func (f *imageFinder) findImageBySemver(ctx context.Context, repo string, spec *yarotskymev1alpha1.VersionStrategySemVerSpec) (*ImageRef, error) {
	if spec == nil {
		return nil, fmt.Errorf("`semver` version strategy spec is empty!")
	}

	constraint, err := semver.NewConstraint(spec.Constraint)
	if err != nil {
		return nil, fmt.Errorf("failed to parse semver constraint %s: %w", spec.Constraint, err)
	}

	ref := ImageRef{Repository: repo}
	tags, err := f.registryClient.ListTags(ctx, ref)
	if err != nil {
		return nil, err
	}

	vs := make([]*semver.Version, 0, len(tags))
	vsToTags := make(map[*semver.Version]string, len(tags))
	for _, tag := range tags {
		v, err := semver.NewVersion(tag)
		if err != nil {
			continue // not a valid semver-like tag; skip
		}

		vs = append(vs, v)
		vsToTags[v] = tag
	}

	sort.Sort(sort.Reverse(semver.Collection(vs)))

	var tag string
	for _, v := range vs {
		if constraint.Check(v) {
			tag = vsToTags[v]
			break
		}
	}

	if tag == "" {
		return nil, fmt.Errorf("%w: no semver-like tags matching constraint %q could be found for repo %q", ErrNotFound, spec.Constraint, repo)
	}

	newRef := ref
	newRef.Tag = tag

	digest, err := f.registryClient.GetDigest(ctx, newRef)
	if err != nil {
		return nil, err
	}
	newRef.Digest = digest

	return &newRef, nil
}
