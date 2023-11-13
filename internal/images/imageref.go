package images

import (
	"fmt"

	"github.com/google/go-containerregistry/pkg/name"
)

type ImageRef struct {
	Repository string
	Tag        string
	Digest     string
}

func (r *ImageRef) String() string {
	if r.Tag != "" && r.Digest != "" {
		return fmt.Sprintf("%s:%s@%s", r.Repository, r.Tag, r.Digest)
	} else if r.Digest != "" {
		return fmt.Sprintf("%s@%s", r.Repository, r.Digest)
	} else if r.Tag != "" {
		return fmt.Sprintf("%s:%s", r.Repository, r.Tag)
	} else {
		return fmt.Sprintf("%s:latest", r.Repository)
	}
}

func (r *ImageRef) ToGoContainerRegistryReference() name.Reference {
	ref, err := name.ParseReference(r.String())
	if err != nil {
		panic(err)
	}
	return ref
}

func (r *ImageRef) ToGoContainerRegistryRepository() name.Repository {
	ref, err := name.ParseReference(r.String())
	if err != nil {
		panic(err)
	}
	return ref.Context()
}
