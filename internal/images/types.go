package images

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

type ImageFinder interface {
	// FindImage takes an image version tracking spec, and returns
	// a new image reference.
	FindImage(ctx context.Context, spec yarotskymev1alpha1.ImageSpec) (*ImageRef, error)
}

type ImageWatcher interface {
	WatchForNewImages(ctx context.Context, c chan event.GenericEvent)
}

type ApplicationLister interface {
	ListApplications(ctx context.Context) ([]yarotskymev1alpha1.Application, error)
}

type ImageCache interface {
	Get(spec yarotskymev1alpha1.ImageSpec) *ImageRef
	Set(spec yarotskymev1alpha1.ImageSpec, ref ImageRef)
	Delete(spec yarotskymev1alpha1.ImageSpec)
	KeepOnly(specs ...yarotskymev1alpha1.ImageSpec) []ImageRef
	Len() int
}

type RegistryClient interface {
	// ListTags lists tags for a given repository.
	// Per spec[^1], tags are returned in lexicographical order.
	// [^1]: https://github.com/opencontainers/distribution-spec/blob/main/spec.md#listing-tags
	ListTags(ctx context.Context, ref ImageRef) ([]string, error)

	// GetDigest returns the current sha256 digest of the given tag for a given repository as a string.
	GetDigest(ctx context.Context, ref ImageRef) (string, error)
}

type ReconcileUpdateScheduleTrigger interface {
	ReconcileUpdateSchedule() <-chan time.Time
}

type CronSchedule string

var (
	ErrNotFound = errors.New("Suitable image not found")
)

type AppJobFn func(ctx context.Context, app *yarotskymev1alpha1.Application)

type Scheduler interface {
	Start(ctx context.Context)
	UpsertJob(app yarotskymev1alpha1.Application, fn AppJobFn) error
	KeepJobs(apps []yarotskymev1alpha1.Application)
}

type ImageSpecKeyType string

func ImageSpecKey(spec yarotskymev1alpha1.ImageSpec) ImageSpecKeyType {
	keyBytes, _ := json.Marshal(spec)
	return ImageSpecKeyType(keyBytes)
}
