package images

import (
	"context"
	"fmt"
	"testing"
	"time"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

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

func TestCronImageWatcher(t *testing.T) {
	app1 := &yarotskymev1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "app1",
		},
		Spec: yarotskymev1alpha1.ApplicationSpec{
			Image: yarotskymev1alpha1.ImageSpec{
				Repository:      "registry.example.com/myimage1",
				VersionStrategy: yarotskymev1alpha1.VersionStrategyDigest,
				Digest: &yarotskymev1alpha1.VersionStrategyDigestSpec{
					Tag: "latest",
				},
				UpdateSchedule: ptr.To("* * * * *"),
			},
		},
	}

	app2 := &yarotskymev1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "app2",
		},
		Spec: yarotskymev1alpha1.ApplicationSpec{
			Image: yarotskymev1alpha1.ImageSpec{
				Repository:      "registry.example.com/myimage2",
				VersionStrategy: yarotskymev1alpha1.VersionStrategyDigest,
				Digest: &yarotskymev1alpha1.VersionStrategyDigestSpec{
					Tag: "latest",
				},
				UpdateSchedule: ptr.To("*/5 * * * *"),
			},
		},
	}

	lister := newFakeApplicationLister()
	finder := newFakeImageFinder()

	trigger := newManualUpdateScheduleTrigger()
	w := NewCronImageWatcher(lister, CronSchedule("* * * * *"), finder, trigger)

	ctx, done := context.WithCancel(context.Background())
	reconcileChan := make(chan event.GenericEvent)
	go w.WatchForNewImages(ctx, reconcileChan)
	defer done()

	// Make sure we're not racing in tests
	w.scheduler.Stop()

	lister.Add(app1)
	lister.Add(app2)

	finder.AddImage(ImageRef{
		Repository: "registry.example.com/myimage1",
		Tag:        "latest",
		Digest:     "sha256:deadbeefdeadbeefdeadbeefdeadbeef",
	})

	finder.AddImage(ImageRef{
		Repository: "registry.example.com/myimage2",
		Tag:        "latest",
		Digest:     "sha256:beefdeadbeefdeadbeefdeadbeefdead",
	})

	// Adds schedules
	trigger.Trigger()
	assert.EventuallyWithT(t, func(t *assert.CollectT) {
		jobs := w.scheduler.Jobs()
		assert.Len(t, jobs, 2)
		assert.ElementsMatch(t, jobs[0].Tags(), []string{"default/app1", "* * * * *"})
		assert.ElementsMatch(t, jobs[1].Tags(), []string{"default/app2", "*/5 * * * *"})
	}, time.Second, 10*time.Millisecond, "expected the schedules to be setup properly")

	// Updates schedules
	app1.Spec.Image.UpdateSchedule = ptr.To("*/2 * * * *")
	trigger.Trigger()
	assert.EventuallyWithT(t, func(t *assert.CollectT) {
		jobs := w.scheduler.Jobs()
		assert.Len(t, jobs, 2)
		assert.ElementsMatch(t, jobs[0].Tags(), []string{"default/app1", "*/2 * * * *"})
		assert.ElementsMatch(t, jobs[1].Tags(), []string{"default/app2", "*/5 * * * *"})
	}, time.Second, 10*time.Millisecond, "expected the schedules to be updated properly")

	w.scheduler.RunAll()

	// Post events to channel
	appsScheduledForReconciliation := map[string]bool{}
	assert.EventuallyWithT(t, func(t *assert.CollectT) {
		select {
		case evt := <-reconcileChan:
			appsScheduledForReconciliation[evt.Object.GetName()] = true
		default:
		}

		assert.Len(t, appsScheduledForReconciliation, 2)
	}, time.Second, 10*time.Millisecond, "expected reconciliation to be triggered for the apps")
	assert.Equal(t, map[string]bool{"app1": true, "app2": true}, appsScheduledForReconciliation)

	assert.EventuallyWithT(t, func(t *assert.CollectT) {
		jobs := w.scheduler.Jobs()
		for _, j := range jobs {
			assert.False(t, j.IsRunning())
		}
	}, time.Second, 10*time.Millisecond, "expected update jobs to settle")

	// Removes schedules of removed apps
	lister.RemoveByReference(app1)
	trigger.Trigger()
	assert.EventuallyWithT(t, func(t *assert.CollectT) {
		jobs := w.scheduler.Jobs()
		assert.Len(t, jobs, 1)
		assert.ElementsMatch(t, jobs[0].Tags(), []string{"default/app2", "*/5 * * * *"})
	}, time.Second, 10*time.Millisecond, "expected the schedule for the removed app to be removed")

	// As a finder, does not hammer image registry
	w.FindImage(ctx, app2.Spec.Image)
	w.FindImage(ctx, app2.Spec.Image)
	w.FindImage(ctx, app2.Spec.Image)

	assert.Equal(t, 1, finder.RepositoryCallCount(app2.Spec.Image.Repository))

	// Allows new lookups on scheduled update

	w.scheduler.RunAll()
	assert.EventuallyWithT(t, func(t *assert.CollectT) {
		jobs := w.scheduler.Jobs()
		for _, j := range jobs {
			assert.False(t, j.IsRunning())
		}
	}, time.Second, 10*time.Millisecond, "expected update jobs to settle")

	w.FindImage(ctx, app2.Spec.Image)
	w.FindImage(ctx, app2.Spec.Image)
	w.FindImage(ctx, app2.Spec.Image)

	assert.Equal(t, 2, finder.RepositoryCallCount(app2.Spec.Image.Repository))
}

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

var _ ReconcileUpdateScheduleTrigger = &manualUpdateScheduleTrigger{}
