package images

import (
	"context"
	"fmt"

	"github.com/google/go-containerregistry/pkg/v1/remote"
)

type RegistryClient interface {
	// ListTags lists tags for a given repository.
	// Per spec[^1], tags are returned in lexicographical order.
	// [^1]: https://github.com/opencontainers/distribution-spec/blob/main/spec.md#listing-tags
	ListTags(ctx context.Context, ref ImageRef) ([]string, error)

	// GetDigest returns the current sha256 digest of the given tag for a given repository as a string.
	GetDigest(ctx context.Context, ref ImageRef) (string, error)
}

type simpleRegistryClient struct {
	extraOpts []remote.Option
}

var _ RegistryClient = &simpleRegistryClient{}

func (c *simpleRegistryClient) ListTags(ctx context.Context, ref ImageRef) ([]string, error) {
	tags, err := remote.List(ref.ToGoContainerRegistryRepository(), c.opts(ctx)...)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch list of tags for %s: %w", ref.Repository, err)
	}
	return tags, nil
}

func (c *simpleRegistryClient) GetDigest(ctx context.Context, ref ImageRef) (string, error) {
	descriptor, err := remote.Get(ref.ToGoContainerRegistryReference(), c.opts(ctx)...)
	if err != nil {
		return "", fmt.Errorf("failed to retrieve digest for %s: %w", ref.String(), err)
	}
	return descriptor.Digest.String(), nil
}

func (c *simpleRegistryClient) opts(ctx context.Context) []remote.Option {
	opts := []remote.Option{remote.WithContext(ctx)}
	opts = append(opts, c.extraOpts...)
	return opts
}
