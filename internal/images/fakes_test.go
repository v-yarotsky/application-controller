package images

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
)

// Fake application lister
var _ ApplicationLister = &fakeApplicationLister{}

func newFakeApplicationLister() *fakeApplicationLister {
	return &fakeApplicationLister{
		applications: make([]*yarotskymev1alpha1.Application, 0),
	}
}

type fakeApplicationLister struct {
	applications []*yarotskymev1alpha1.Application
}

func (lister *fakeApplicationLister) ListApplications(ctx context.Context) ([]yarotskymev1alpha1.Application, error) {
	result := make([]yarotskymev1alpha1.Application, len(lister.applications))
	for i, app := range lister.applications {
		result[i] = *app.DeepCopy()
	}
	return result, nil
}

func (lister *fakeApplicationLister) Add(app *yarotskymev1alpha1.Application) {
	lister.applications = append(lister.applications, app)
}

func (lister *fakeApplicationLister) RemoveByReference(appToRemove *yarotskymev1alpha1.Application) {
	apps := make([]*yarotskymev1alpha1.Application, 0, len(lister.applications))
	for _, app := range lister.applications {
		if app != appToRemove {
			apps = append(apps, app)
		}
	}
	lister.applications = apps
}

// Fake image finder
var _ ImageFinder = &fakeImageFinder{}

func newFakeImageFinder() *fakeImageFinder {
	return &fakeImageFinder{
		images:          make(map[string]*ImageRef, 10),
		repositoryCalls: make(map[string]int, 10),
	}
}

type fakeImageFinder struct {
	images          map[string]*ImageRef
	repositoryCalls map[string]int
}

func (f *fakeImageFinder) FindImage(ctx context.Context, spec yarotskymev1alpha1.ImageSpec) (*ImageRef, error) {
	f.repositoryCalls[spec.Repository] = f.repositoryCalls[spec.Repository] + 1

	if ref, ok := f.images[spec.Repository]; ok {
		return ref, nil
	} else {
		return nil, fmt.Errorf("Image not found: %w", ErrNotFound)
	}
}

func (f *fakeImageFinder) RepositoryCallCount(repository string) int {
	return f.repositoryCalls[repository]
}

func (f *fakeImageFinder) AddImage(ref ImageRef) {
	f.images[ref.Repository] = &ref
}

// Fake schedule reconcile trigger
var _ ReconcileUpdateScheduleTrigger = &manualUpdateScheduleTrigger{}

func newManualUpdateScheduleTrigger() *manualUpdateScheduleTrigger {
	return &manualUpdateScheduleTrigger{
		c: make(chan time.Time),
	}
}

type manualUpdateScheduleTrigger struct {
	c chan time.Time
}

func (t *manualUpdateScheduleTrigger) ReconcileUpdateSchedule() <-chan time.Time {
	return t.c
}

func (t *manualUpdateScheduleTrigger) Trigger() {
	t.c <- time.Now()
}

// Fake update check scheduler
var _ Scheduler = &fakeScheduler{}

func newFakeScheduler(t *testing.T) *fakeScheduler {
	return &fakeScheduler{
		jobs: make(map[schedulerKey]func()),
		t:    t,
	}
}

type schedulerKey struct {
	appName  string
	schedule CronSchedule
}

type fakeScheduler struct {
	jobs map[schedulerKey]func()
	t    *testing.T
}

func (s *fakeScheduler) Start(ctx context.Context) {
}

func (s *fakeScheduler) UpsertJob(app yarotskymev1alpha1.Application, fn AppJobFn) error {
	k := s.key(&app)
	s.t.Logf("Upsert job: %#v", k)
	s.jobs[k] = func() { fn(context.Background(), app.DeepCopy()) }
	return nil
}

func (s *fakeScheduler) KeepJobs(apps []yarotskymev1alpha1.Application) {
	keep := make(map[schedulerKey]func(), len(s.jobs))
	for _, app := range apps {
		k := s.key(&app)
		keep[k] = s.jobs[k]
	}
	s.jobs = keep
}

func (s *fakeScheduler) RunAllScheduledJobsNow() (waitFn func()) {
	wg := sync.WaitGroup{}
	for _, fn := range s.jobs {
		wg.Add(1)
		go func(f func()) {
			defer wg.Done()
			f()
		}(fn)
	}
	return wg.Wait
}

func (s *fakeScheduler) key(app *yarotskymev1alpha1.Application) schedulerKey {
	n := types.NamespacedName{Namespace: app.Namespace, Name: app.Name}
	return schedulerKey{appName: n.String(), schedule: CronSchedule(*app.Spec.Image.UpdateSchedule)}
}
