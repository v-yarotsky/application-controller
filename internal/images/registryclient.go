package images

import (
	"context"
	"fmt"

	"git.home.yarotsky.me/vlad/application-controller/internal/metrics"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

var _ RegistryClient = &simpleRegistryClient{}

type simpleRegistryClient struct {
	extraOpts []remote.Option
}

func (c *simpleRegistryClient) ListTags(ctx context.Context, ref ImageRef) ([]string, error) {
	tags, err := remote.List(ref.ToGoContainerRegistryRepository(), c.opts(ctx)...)
	c.instrumentLookup(ref, "list_tags", err)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch list of tags for %s: %w", ref.Repository, err)
	}
	return tags, nil
}

func (c *simpleRegistryClient) GetDigest(ctx context.Context, ref ImageRef) (string, error) {
	descriptor, err := remote.Get(ref.ToGoContainerRegistryReference(), c.opts(ctx)...)
	c.instrumentLookup(ref, "get_digest", err)
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

func (c *simpleRegistryClient) instrumentLookup(ref ImageRef, method string, err error) {
	imageContext := ref.ToGoContainerRegistryReference().Context()
	successLabel := "true"
	if err != nil {
		successLabel = "false"
	}
	metrics.ImageRegistryCalls.WithLabelValues(
		imageContext.RegistryStr(),
		imageContext.RepositoryStr(),
		method, successLabel,
	).Inc()
}
