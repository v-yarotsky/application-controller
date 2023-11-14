package images

import (
	"context"
	"fmt"

	"github.com/google/go-containerregistry/pkg/v1/remote"
)

var _ RegistryClient = &simpleRegistryClient{}

type simpleRegistryClient struct {
	extraOpts []remote.Option
}

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
