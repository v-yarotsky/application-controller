package images

import (
	"context"
	"fmt"
	"sync"
	"time"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	"github.com/go-co-op/gocron"
	"github.com/go-logr/logr"
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
		NewCronScheduler(CronSchedule(defaultSchedule)),
		imageFinder,
		NewPeriodicReconcileUpdateScheduleTrigger(scheduleReconcileInterval),
	), nil
}

func NewCronImageWatcher(lister ApplicationLister, scheduler Scheduler, imageFinder ImageFinder, scheduleReconcileTrigger ReconcileUpdateScheduleTrigger) *cronImageWatcher {
	return &cronImageWatcher{
		imageCache:               NewInMemoryImageCache(),
		imageFinder:              imageFinder,
		lister:                   lister,
		scheduleReconcileTrigger: scheduleReconcileTrigger,
		scheduler:                scheduler,
		updateChan:               make(chan *yarotskymev1alpha1.Application),
	}
}

type cronImageWatcher struct {
	imageCache               ImageCache
	imageFinder              ImageFinder
	lister                   ApplicationLister
	scheduleReconcileTrigger ReconcileUpdateScheduleTrigger
	scheduler                Scheduler
	updateChan               chan *yarotskymev1alpha1.Application
}

func (w *cronImageWatcher) WatchForNewImages(ctx context.Context, c chan event.GenericEvent) {
	go w.scheduler.Start(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.scheduleReconcileTrigger.ReconcileUpdateSchedule():
			w.reconcileSchedules(ctx)
		case app := <-w.updateChan:
			c <- event.GenericEvent{Object: app}
		}
	}
}

func (w *cronImageWatcher) FindImage(ctx context.Context, spec yarotskymev1alpha1.ImageSpec) (*ImageRef, error) {
	log := log.FromContext(ctx).WithValues("repo", spec.Repository)

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
	log := log.FromContext(ctx)
	log.Info("Reconciling application update schedules")

	apps, err := w.lister.ListApplications(ctx)
	if err != nil {
		log.Error(err, "failed to reconcile Application update schedules")
		return
	}

	seenImageSpecs := make([]yarotskymev1alpha1.ImageSpec, 0, len(apps))

	for _, app := range apps {
		seenImageSpecs = append(seenImageSpecs, app.Spec.Image)
		if err := w.scheduler.UpsertJob(app, w.enqueueReconciliation); err != nil {
			log.Error(err, "failed to upsert update check job")
		}
	}

	w.scheduler.KeepJobs(apps)

	prunedImageRefs := w.imageCache.KeepOnly(seenImageSpecs...)
	if len(prunedImageRefs) > 0 {
		log.WithValues("prunedImageRefs", len(prunedImageRefs)).Info("Pruned image cache")
	}
}

func (w *cronImageWatcher) enqueueReconciliation(ctx context.Context, app *yarotskymev1alpha1.Application) {
	log := log.FromContext(ctx)

	log.Info("Clearing cached image")
	w.imageCache.Delete(app.Spec.Image)

	log.Info("Scheduling reconciliation to check for image updates")
	w.updateChan <- app
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

var _ Scheduler = &cronScheduler{}

type jobHandle struct {
	gocronJob *gocron.Job
	schedule  CronSchedule
}

type cronScheduler struct {
	ctx             context.Context
	defaultSchedule CronSchedule
	jobs            map[string]jobHandle
	lock            sync.RWMutex
	log             logr.Logger
	scheduler       *gocron.Scheduler
	isStarted       bool
}

func NewCronScheduler(defaultSchedule CronSchedule) *cronScheduler {
	scheduler := gocron.NewScheduler(time.Local)
	scheduler.SingletonModeAll()
	scheduler.WaitForScheduleAll()

	return &cronScheduler{
		defaultSchedule: defaultSchedule,
		jobs:            make(map[string]jobHandle),
		lock:            sync.RWMutex{},
		scheduler:       scheduler,
	}
}

func (s *cronScheduler) Start(ctx context.Context) {
	s.ctx = ctx
	s.log = log.FromContext(ctx)
	s.isStarted = true
	log := s.log

	log.Info("Starting Cron scheduler")
	s.scheduler.StartAsync()
	for {
		select {
		case <-ctx.Done():
			log.Info("Stopping Cron scheduler")
			s.scheduler.Stop()
			return
		}
	}
}

func (s *cronScheduler) UpsertJob(app yarotskymev1alpha1.Application, fn AppJobFn) error {
	if !s.isStarted {
		return fmt.Errorf("Scheduler is not started, can't upsert jobs")
	}

	key := s.key(&app)
	schedule := s.scheduleOrDefault(&app)

	log := s.log.WithValues("key", key, "schedule", schedule)

	s.lock.RLock()
	handle, ok := s.jobs[key]
	s.lock.RUnlock()

	if ok {
		if handle.schedule == schedule {
			log.Info("No changes to existing schedule")
			return nil
		}
		log.Info("Existing update schedule has changed; cleaning.")
		s.scheduler.RemoveByReference(handle.gocronJob)
	}

	log.Info("Setting new update schedule")
	ctx := logr.NewContext(s.ctx, log)
	gocronJob, err := s.scheduler.Cron(string(schedule)).Do(fn, ctx, app.DeepCopy())
	if err != nil {
		return fmt.Errorf("failed to schedule update check %s: %w", key, err)
	}

	s.lock.Lock()
	s.jobs[key] = jobHandle{schedule: schedule, gocronJob: gocronJob}
	s.lock.Unlock()

	return nil
}

func (s *cronScheduler) KeepJobs(apps []yarotskymev1alpha1.Application) {
	log := s.log

	keepSet := make(map[string]bool, len(apps))

	for _, app := range apps {
		keepSet[s.key(&app)] = true
	}

	discard := make([]string, 0, len(apps))

	s.lock.RLock()
	for k := range s.jobs {
		if keepSet[k] {
			continue
		}
		discard = append(discard, k)
	}
	s.lock.RUnlock()

	s.lock.Lock()
	defer s.lock.Unlock()

	for _, k := range discard {
		h := s.jobs[k]
		log.WithValues("key", k, "schedule", h.schedule).Info("Unscheduling job")
		delete(s.jobs, k)
	}
}

func (s *cronScheduler) key(app *yarotskymev1alpha1.Application) string {
	return types.NamespacedName{Namespace: app.Namespace, Name: app.Name}.String()
}

func (s *cronScheduler) scheduleOrDefault(app *yarotskymev1alpha1.Application) CronSchedule {
	if schedule := app.Spec.Image.UpdateSchedule; schedule != nil {
		return CronSchedule(*schedule)
	} else {
		return s.defaultSchedule
	}
}
