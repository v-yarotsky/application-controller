package images

import (
	"context"
	"fmt"
	"time"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	"github.com/go-co-op/gocron"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func NewCronImageWatcherWithDefaults(client client.Client, defaultSchedule string, imagePullSecrets []string, scheduleReconcileInterval time.Duration) (*cronImageWatcher, error) {
	imageFinder, err := NewImageFinder(
		WithAuthenticatedRegistryClient(imagePullSecrets),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to instantiate image finder: %w", err)
	}

	lister := NewSimpleApplicationLister(client)
	return NewCronImageWatcher(
		lister,
		CronSchedule(defaultSchedule),
		imageFinder,
		NewPeriodicReconcileUpdateScheduleTrigger(scheduleReconcileInterval),
	), nil
}

func NewCronImageWatcher(lister ApplicationLister, defaultSchedule CronSchedule, imageFinder ImageFinder, scheduleReconcileTrigger ReconcileUpdateScheduleTrigger) *cronImageWatcher {
	scheduler := gocron.NewScheduler(time.Local)
	scheduler.SingletonModeAll()
	scheduler.WaitForScheduleAll()

	gocron.SetPanicHandler(func(jobName string, data interface{}) {
		fmt.Printf("Panic in job: %s: %#v\n", jobName, data)
	})

	return &cronImageWatcher{
		lister:                   lister,
		defaultSchedule:          defaultSchedule,
		scheduler:                scheduler,
		scheduleReconcileTrigger: scheduleReconcileTrigger,
		jobCache:                 NewInMemoryJobCache(),
		imageCache:               NewInMemoryImageCache(),
		updateChan:               make(chan *yarotskymev1alpha1.Application),
		imageFinder:              imageFinder,
	}
}

type cronImageWatcher struct {
	lister                   ApplicationLister
	defaultSchedule          CronSchedule
	scheduler                *gocron.Scheduler
	scheduleReconcileTrigger ReconcileUpdateScheduleTrigger
	jobCache                 JobCache
	imageCache               ImageCache
	updateChan               chan *yarotskymev1alpha1.Application
	imageFinder              ImageFinder
}

func (w *cronImageWatcher) WatchForNewImages(ctx context.Context, c chan event.GenericEvent) {
	w.start(ctx)

	for {
		select {
		case <-ctx.Done():
			w.stop(ctx)
			return
		case <-w.scheduleReconcileTrigger.ReconcileUpdateSchedule():
			w.reconcileSchedules(ctx)
		case app := <-w.updateChan:
			c <- event.GenericEvent{Object: app}
		}
	}
}

func (w *cronImageWatcher) FindImage(ctx context.Context, spec yarotskymev1alpha1.ImageSpec) (*ImageRef, error) {
	log := log.FromContext(ctx).WithValues("repo", spec.Repository, "schedule", w.schedule(spec))

	cached := w.imageCache.Get(spec)
	if cached != nil {
		log.WithValues("image", cached).Info("found cached image")
		return cached, nil
	}

	log.Info("No cached image found, fetching")
	imgRef, err := w.imageFinder.FindImage(ctx, spec)
	if err != nil {
		err := fmt.Errorf("failed to find the latest suitable image: %w", err)
		log.Error(err, "failed to find a suitable image")
		return nil, err
	}
	log.WithValues("image", imgRef).Info("Caching image")
	w.imageCache.Set(spec, *imgRef)
	return imgRef, nil
}

func (w *cronImageWatcher) reconcileSchedules(ctx context.Context) {
	baseLog := log.FromContext(ctx)
	log := baseLog

	log.Info("Reconciling application update schedules")

	apps, err := w.lister.ListApplications(ctx)
	if err != nil {
		log.Error(err, "failed to reconcile Application update schedules")
		return
	}

	seenAppNames := make([]types.NamespacedName, 0, len(apps))
	seenImageSpecs := make([]yarotskymev1alpha1.ImageSpec, 0, len(apps))

	for _, app := range apps {
		appSchedule := w.schedule(app.Spec.Image)
		name := appName(&app)

		seenAppNames = append(seenAppNames, name)
		seenImageSpecs = append(seenImageSpecs, app.Spec.Image)

		log = baseLog.WithValues("name", name, "schedule", string(appSchedule))
		if job := w.jobCache.Get(name); job != nil {
			if job.Schedule == appSchedule {
				log.Info("No changes to existing schedule")
				continue
			}
			log.Info("Existing update schedule has changed; cleaning.")
			w.scheduler.RemoveByReference(job.CronJob)
		}
		log.Info("Setting new update schedule")
		job, err := w.scheduleUpdateCheck(ctx, &app)
		if err != nil {
			log.Error(err, "failed to schedule update check")
			return
		}
		w.jobCache.Set(name, job)
	}

	log = baseLog

	prunedJobs := w.jobCache.KeepOnly(seenAppNames...)

	if len(prunedJobs) > 0 {
		log.WithValues("prunedJobs", len(prunedJobs)).Info("Pruned cron jobs")
		for _, j := range prunedJobs {
			w.scheduler.RemoveByReference(j.CronJob)
		}
	}

	prunedImageRefs := w.imageCache.KeepOnly(seenImageSpecs...)
	if len(prunedImageRefs) > 0 {
		log.WithValues("prunedImageRefs", len(prunedImageRefs)).Info("Pruned image cache")
	}
}

func (w *cronImageWatcher) scheduleUpdateCheck(ctx context.Context, app *yarotskymev1alpha1.Application) (*Job, error) {
	appSchedule := w.schedule(app.Spec.Image)

	job, err := w.scheduler.Cron(string(appSchedule)).Tag(appName(app).String(), string(appSchedule)).DoWithJobDetails(w.enqueueReconciliation, ctx, app.DeepCopy())
	if err != nil {
		return nil, fmt.Errorf("failed to schedule update check for app %s: %w", appName(app), err)
	}
	return &Job{appSchedule, job}, nil
}

func (w *cronImageWatcher) enqueueReconciliation(ctx context.Context, app *yarotskymev1alpha1.Application, job gocron.Job) {
	log := log.FromContext(ctx).WithValues("name", appName(app), "schedule", w.schedule(app.Spec.Image), "lastRun", job.LastRun())

	log.Info("Clearing cached image")
	w.imageCache.Delete(app.Spec.Image)

	log.Info("Scheduling reconciliation to check for image updates")
	w.updateChan <- app
}

func (w *cronImageWatcher) start(ctx context.Context) {
	log := log.FromContext(ctx)
	log.Info("Starting image watcher")
	w.scheduler.StartAsync()
}

func (w *cronImageWatcher) stop(ctx context.Context) {
	log := log.FromContext(ctx)
	log.Info("Stopping image watcher")
	w.scheduler.Stop()
}

func (w *cronImageWatcher) schedule(spec yarotskymev1alpha1.ImageSpec) CronSchedule {
	if s := spec.UpdateSchedule; s != nil {
		return CronSchedule(*s)
	} else {
		return w.defaultSchedule
	}
}

var _ ImageWatcher = &cronImageWatcher{}
var _ ImageFinder = &cronImageWatcher{}

func appName(app *yarotskymev1alpha1.Application) types.NamespacedName {
	return types.NamespacedName{Namespace: app.Namespace, Name: app.Name}
}

type periodicReconcileUpdateScheduleTrigger struct {
	interval time.Duration
}

func NewPeriodicReconcileUpdateScheduleTrigger(interval time.Duration) *periodicReconcileUpdateScheduleTrigger {
	return &periodicReconcileUpdateScheduleTrigger{
		interval: interval,
	}
}

func (t *periodicReconcileUpdateScheduleTrigger) ReconcileUpdateSchedule() <-chan time.Time {
	return time.After(t.interval)
}

var _ ReconcileUpdateScheduleTrigger = &periodicReconcileUpdateScheduleTrigger{}
