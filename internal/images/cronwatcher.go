package images

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	"github.com/go-co-op/gocron"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type imageSpecKey struct {
	yarotskymev1alpha1.ImageSpec
}

func (t imageSpecKey) CacheKey() string {
	keyBytes, _ := json.Marshal(t)
	return string(keyBytes)
}

type jobKey struct {
	*yarotskymev1alpha1.Application
}

func (t jobKey) CacheKey() string {
	return types.NamespacedName{Namespace: t.Namespace, Name: t.Name}.String()
}

func NewCronImageWatcherWithDefaults(client client.Client, defaultSchedule CronSchedule, imagePullSecrets []string, scheduleReconcileInterval time.Duration) (*cronImageWatcher, error) {
	imageFinder, err := NewImageFinder(
		WithAuthenticatedRegistryClient(imagePullSecrets),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to instantiate image finder: %w", err)
	}

	lister := NewSimpleApplicationLister(client)
	return NewCronImageWatcher(
		lister,
		NewCronScheduler(defaultSchedule),
		imageFinder,
		NewPeriodicReconcileUpdateScheduleTrigger(scheduleReconcileInterval),
	), nil
}

func NewCronImageWatcher(lister ApplicationLister, scheduler Scheduler, imageFinder ImageFinder, scheduleReconcileTrigger ReconcileUpdateScheduleTrigger) *cronImageWatcher {
	return &cronImageWatcher{
		imageCache:               NewCache[imageSpecKey, ImageRef](),
		imageFinder:              imageFinder,
		lister:                   lister,
		scheduleReconcileTrigger: scheduleReconcileTrigger,
		scheduler:                scheduler,
		updateChan:               make(chan *yarotskymev1alpha1.Application),
	}
}

type cronImageWatcher struct {
	imageCache               *cache[imageSpecKey, ImageRef]
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

type RegistryNotification struct {
	Events []struct {
		Action string `json:"action"`
		Target struct {
			Repository string `json:"repository"`
		} `json:"target"`
	} `json:"events"`
}

func (cw *cronImageWatcher) ServeWebhook(ctx context.Context) {
	mux := http.NewServeMux()
	mux.HandleFunc("/webhooks/image", func(w http.ResponseWriter, r *http.Request) {
		log := log.FromContext(r.Context())
		notification := RegistryNotification{}
		err := json.NewDecoder(r.Body).Decode(&notification)
		if err != nil {
			log.Error(err, "failed to parse webhook payload")
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		updatedRepositories := make(map[string]bool, 0)
		for _, e := range notification.Events {
			if e.Action != "push" {
				continue
			}
			updatedRepositories[e.Target.Repository] = true
		}
		for repo := range updatedRepositories {
			cw.onImageUpdated(r.Context(), repo)
		}
	})
	s := &http.Server{
		Addr:        ":3000",
		Handler:     mux,
		BaseContext: func(l net.Listener) context.Context { return ctx },
	}
	s.ListenAndServe()
}

func (w *cronImageWatcher) FindImage(ctx context.Context, spec yarotskymev1alpha1.ImageSpec) (*ImageRef, error) {
	log := log.FromContext(ctx).WithValues("repo", spec.Repository)

	cached := w.imageCache.Get(imageSpecKey{spec})
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
	w.imageCache.Set(imageSpecKey{spec}, *imgRef)
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

	seenImageSpecKeys := make([]imageSpecKey, 0, len(apps))

	for _, app := range apps {
		seenImageSpecKeys = append(seenImageSpecKeys, imageSpecKey{app.Spec.Image})
		if err := w.scheduler.UpsertJob(app, w.enqueueReconciliation); err != nil {
			log.Error(err, "failed to upsert update check job")
		}
	}

	w.scheduler.KeepJobs(apps)

	prunedImageRefs := w.imageCache.KeepOnly(seenImageSpecKeys...)
	if len(prunedImageRefs) > 0 {
		log.WithValues("prunedImageRefs", len(prunedImageRefs)).Info("Pruned image cache")
	}
}

func (w *cronImageWatcher) enqueueReconciliation(ctx context.Context, app *yarotskymev1alpha1.Application) {
	log := log.FromContext(ctx).WithValues("namespace", app.Namespace, "app", app.Name)

	log.Info("Clearing cached image")
	w.imageCache.Delete(imageSpecKey{app.Spec.Image})

	log.Info("Scheduling reconciliation to check for image updates")
	w.updateChan <- app
}

func (w *cronImageWatcher) onImageUpdated(ctx context.Context, imageRepo string) {
	log := log.FromContext(ctx).WithValues("repository", imageRepo)

	log.Info("Clearing cache images")
	apps, err := w.lister.ListApplications(ctx)
	if err != nil {
		log.Error(err, "failed to eagerly reconcile Applications on container publish")
		return
	}

	for _, app := range apps {
		imageSpec := app.Spec.Image
		if imageSpec.Repository == imageRepo {
			app := app
			go w.enqueueReconciliation(ctx, &app)
		}
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

var _ Scheduler = &cronScheduler{}

type jobHandle struct {
	gocronJob *gocron.Job
	schedule  CronSchedule
}

type cronScheduler struct {
	ctx             context.Context
	defaultSchedule CronSchedule
	jobs            *cache[jobKey, jobHandle]
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
		jobs:            NewCache[jobKey, jobHandle](),
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

	key := jobKey{&app}
	schedule := s.scheduleOrDefault(&app)

	log := s.log.WithValues("key", key.CacheKey(), "schedule", schedule)

	handle := s.jobs.Get(key)

	if handle != nil {
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
		return fmt.Errorf("failed to schedule update check %s: %w", key.CacheKey(), err)
	}

	s.jobs.Set(key, jobHandle{schedule: schedule, gocronJob: gocronJob})

	return nil
}

func (s *cronScheduler) KeepJobs(apps []yarotskymev1alpha1.Application) {
	log := s.log

	keys := make([]jobKey, len(apps))
	for i, app := range apps {
		app := app
		keys[i] = jobKey{&app}
	}

	discarded := s.jobs.KeepOnly(keys...)

	for k, h := range discarded {
		log.WithValues("key", k, "schedule", h.schedule).Info("Unscheduling job")
		s.scheduler.RemoveByReference(h.gocronJob)
	}
}

func (s *cronScheduler) scheduleOrDefault(app *yarotskymev1alpha1.Application) CronSchedule {
	if schedule := app.Spec.Image.UpdateSchedule; schedule != nil {
		return CronSchedule(*schedule)
	} else {
		return s.defaultSchedule
	}
}
