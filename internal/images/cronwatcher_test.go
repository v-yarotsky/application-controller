package images

import (
	"context"
	"testing"
	"time"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

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
	scheduler := newFakeScheduler(t)
	w := NewCronImageWatcher(lister, scheduler, finder, trigger)

	ctx, done := context.WithCancel(context.Background())
	reconcileChan := make(chan event.GenericEvent)
	go w.WatchForNewImages(ctx, reconcileChan)
	defer done()

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
		assert.Len(t, scheduler.jobs, 2)
		assert.Contains(t, scheduler.jobs, schedulerKey{"default/app1", "* * * * *"})
		assert.Contains(t, scheduler.jobs, schedulerKey{"default/app2", "*/5 * * * *"})
	}, 10*time.Millisecond, time.Millisecond, "expected app update schedules to be set")

	// Updates schedules
	app1.Spec.Image.UpdateSchedule = ptr.To("*/2 * * * *")
	trigger.Trigger()

	assert.EventuallyWithT(t, func(t *assert.CollectT) {
		assert.Len(t, scheduler.jobs, 2)
		assert.Contains(t, scheduler.jobs, schedulerKey{"default/app1", "*/2 * * * *"})
		assert.Contains(t, scheduler.jobs, schedulerKey{"default/app2", "*/5 * * * *"})
	}, 10*time.Millisecond, time.Millisecond, "expected app update schedules to be updated")

	wait := scheduler.RunAllScheduledJobsNow()

	// Post events to channel
	appsScheduledForReconciliation := map[string]bool{}
	assert.EventuallyWithT(t, func(t *assert.CollectT) {
		select {
		case evt := <-reconcileChan:
			appsScheduledForReconciliation[evt.Object.GetName()] = true
		default:
		}

		assert.Len(t, appsScheduledForReconciliation, 2)
	}, 10*time.Millisecond, time.Millisecond, "expected reconciliation to be triggered for the apps")
	assert.Equal(t, map[string]bool{"app1": true, "app2": true}, appsScheduledForReconciliation)
	wait()

	// Removes schedules of removed apps
	lister.RemoveByReference(app1)
	trigger.Trigger()

	assert.EventuallyWithT(t, func(t *assert.CollectT) {
		assert.Len(t, scheduler.jobs, 1)
		assert.Contains(t, scheduler.jobs, schedulerKey{"default/app2", "*/5 * * * *"})
	}, 10*time.Millisecond, time.Millisecond, "expected reconciliation to be triggered for the apps")

	// As a finder, does not hammer image registry
	w.FindImage(ctx, app2.Spec.Image)
	w.FindImage(ctx, app2.Spec.Image)
	w.FindImage(ctx, app2.Spec.Image)

	assert.Equal(t, 1, finder.RepositoryCallCount(app2.Spec.Image.Repository))

	// Allows new lookups on scheduled update
	wait = scheduler.RunAllScheduledJobsNow()
	wait()

	w.FindImage(ctx, app2.Spec.Image)
	w.FindImage(ctx, app2.Spec.Image)
	w.FindImage(ctx, app2.Spec.Image)

	assert.Equal(t, 2, finder.RepositoryCallCount(app2.Spec.Image.Repository))
}
