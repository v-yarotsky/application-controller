package images

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	"github.com/go-co-op/gocron"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type ImageWatcher interface {
	WatchForNewImages(ctx context.Context, c chan event.GenericEvent)
}

func NewSillyImageWatcher(client client.Client) *sillyImageWatcher {
	return &sillyImageWatcher{
		client:   client,
		interval: 5 * time.Minute,
	}
}

var _ ImageWatcher = &sillyImageWatcher{}

type sillyImageWatcher struct {
	client   client.Client
	interval time.Duration
}

func (w *sillyImageWatcher) WatchForNewImages(ctx context.Context, c chan event.GenericEvent) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(w.interval):
			w.enqueueAllApplications(ctx, c)
		}
	}
}

func (w *sillyImageWatcher) enqueueAllApplications(ctx context.Context, c chan event.GenericEvent) {
	log := log.FromContext(ctx)

	log.Info("Listing Applications to check for updates")

	// TODO pagination
	list := &metav1.PartialObjectMetadataList{}
	list.SetGroupVersionKind(yarotskymev1alpha1.SchemeBuilder.GroupVersion.WithKind("ApplicationList"))

	lookInAllNamespaces := client.InNamespace("")
	err := w.client.List(context.Background(), list, lookInAllNamespaces)
	if err != nil {
		log.Error(err, "failed to get the list of Applications to check for image updates")
		return
	}
	for _, app := range list.Items {
		log.WithValues("namespace", app.Namespace, "name", app.Name).Info("Checking for updated Application image")
		c <- event.GenericEvent{Object: &app}
	}
}

var _ ImageWatcher = &cronImageWatcher{}
var _ ImageFinder = &cronImageWatcher{}

type CronSchedule string

func NewCronImageWatcher(client client.Client, defaultSchedule CronSchedule, imageFinder ImageFinder) *cronImageWatcher {
	scheduler := gocron.NewScheduler(time.Local)
	scheduler.SingletonModeAll()
	scheduler.WaitForScheduleAll()

	gocron.SetPanicHandler(func(jobName string, data interface{}) {
		fmt.Printf("Panic in job: %s: %#v\n", jobName, data)
	})

	return &cronImageWatcher{
		client:            client,
		defaultSchedule:   defaultSchedule,
		scheduler:         scheduler,
		reconcileInterval: 30 * time.Second,
		jobCache:          NewInMemoryJobCache(),
		imageCache:        NewInMemoryImageCache(),
		updateChan:        make(chan *yarotskymev1alpha1.Application),
		imageFinder:       imageFinder,
	}
}

type cronImageWatcher struct {
	client            client.Client
	defaultSchedule   CronSchedule
	scheduler         *gocron.Scheduler
	reconcileInterval time.Duration
	jobCache          JobCache
	imageCache        ImageCache
	updateChan        chan *yarotskymev1alpha1.Application
	imageFinder       ImageFinder
}

func (w *cronImageWatcher) WatchForNewImages(ctx context.Context, c chan event.GenericEvent) {
	w.start(ctx)
	w.reconcileSchedules(ctx)

	for {
		select {
		case <-ctx.Done():
			w.stop(ctx)
		case <-time.After(w.reconcileInterval): // TODO: ideally, every time we see a create/delete or ImageSpec change
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

	list := &yarotskymev1alpha1.ApplicationList{}
	lookInAllNamespaces := client.InNamespace("")

	err := w.client.List(context.Background(), list, lookInAllNamespaces)
	if err != nil {
		log.Error(err, "failed to get the list of Applications to check for image updates")
		return
	}

	seenAppNames := make([]types.NamespacedName, 0, len(list.Items))
	seenImageSpecs := make([]yarotskymev1alpha1.ImageSpec, 0, len(list.Items))

	for _, app := range list.Items {
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

	prunedJobs := w.jobCache.KeepOnly(seenAppNames...)

	log = baseLog

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

	job, err := w.scheduler.Cron(string(appSchedule)).Tag(appName(app).String(), string(appSchedule)).DoWithJobDetails(w.enqueueReconciliation, ctx, app)
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

func appName(app *yarotskymev1alpha1.Application) types.NamespacedName {
	return types.NamespacedName{Namespace: app.Namespace, Name: app.Name}
}

type ImageCache interface {
	Get(spec yarotskymev1alpha1.ImageSpec) *ImageRef
	Set(spec yarotskymev1alpha1.ImageSpec, ref ImageRef)
	Delete(spec yarotskymev1alpha1.ImageSpec)
	KeepOnly(specs ...yarotskymev1alpha1.ImageSpec) []ImageRef
	Len() int
}

type inMemoryImageCache struct {
	cache map[string]ImageRef
	lock  sync.RWMutex
}

func NewInMemoryImageCache() *inMemoryImageCache {
	return &inMemoryImageCache{
		cache: make(map[string]ImageRef),
		lock:  sync.RWMutex{},
	}
}

var _ ImageCache = &inMemoryImageCache{}

func (c *inMemoryImageCache) Get(spec yarotskymev1alpha1.ImageSpec) *ImageRef {
	c.lock.RLock()
	defer c.lock.RUnlock()

	if result, ok := c.cache[c.key(spec)]; ok {
		return &result
	}
	return nil
}

func (c *inMemoryImageCache) Set(spec yarotskymev1alpha1.ImageSpec, ref ImageRef) {
	c.lock.Lock()
	defer c.lock.Unlock()

	c.cache[c.key(spec)] = ref
}

func (c *inMemoryImageCache) Delete(spec yarotskymev1alpha1.ImageSpec) {
	c.lock.Lock()
	defer c.lock.Unlock()

	delete(c.cache, c.key(spec))
}

func (c *inMemoryImageCache) KeepOnly(specs ...yarotskymev1alpha1.ImageSpec) []ImageRef {
	c.lock.Lock()
	defer c.lock.Unlock()

	keys := make(map[string]bool, len(specs))
	for _, spec := range specs {
		keys[c.key(spec)] = true
	}

	unwantedKeys := make([]string, 0, len(specs))
	for k := range c.cache {
		if keys[k] {
			continue
		}
		unwantedKeys = append(unwantedKeys, k)
	}

	prunedImageRefs := make([]ImageRef, 0, len(unwantedKeys))
	for _, k := range unwantedKeys {
		prunedImageRefs = append(prunedImageRefs, c.cache[k])
		delete(c.cache, k)
	}

	return prunedImageRefs
}

func (c *inMemoryImageCache) Len() int {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return len(c.cache)
}

func (c *inMemoryImageCache) key(spec yarotskymev1alpha1.ImageSpec) string {
	keyBytes, _ := json.Marshal(spec)
	return string(keyBytes)
}

type Job struct {
	Schedule CronSchedule
	CronJob  *gocron.Job
}

type JobCache interface {
	Get(appName types.NamespacedName) *Job
	Set(appName types.NamespacedName, job *Job)
	Delete(appName types.NamespacedName)
	KeepOnly(appNames ...types.NamespacedName) []*Job
	Len() int
}

type inMemoryJobCache struct {
	cache map[types.NamespacedName]*Job
	lock  sync.RWMutex
}

var _ JobCache = &inMemoryJobCache{}

func NewInMemoryJobCache() JobCache {
	return &inMemoryJobCache{
		cache: make(map[types.NamespacedName]*Job),
		lock:  sync.RWMutex{},
	}
}

func (c *inMemoryJobCache) Get(appName types.NamespacedName) *Job {
	c.lock.RLock()
	defer c.lock.RUnlock()

	return c.cache[appName]
}

func (c *inMemoryJobCache) Set(appName types.NamespacedName, job *Job) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.cache[appName] = job
}

func (c *inMemoryJobCache) Delete(appName types.NamespacedName) {
	c.lock.Lock()
	defer c.lock.Unlock()
	delete(c.cache, appName)
}

func (c *inMemoryJobCache) KeepOnly(appNames ...types.NamespacedName) []*Job {
	c.lock.Lock()
	defer c.lock.Unlock()
	keys := make(map[types.NamespacedName]bool, len(appNames))
	for _, n := range appNames {
		keys[n] = true
	}

	unwantedKeys := make([]types.NamespacedName, 0, len(appNames))
	for k := range c.cache {
		if keys[k] {
			continue
		}
		unwantedKeys = append(unwantedKeys, k)
	}

	prunedJobs := make([]*Job, 0, len(unwantedKeys))

	for _, k := range unwantedKeys {
		prunedJobs = append(prunedJobs, c.cache[k])
		delete(c.cache, k)
	}
	return prunedJobs
}

func (c *inMemoryJobCache) Len() int {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return len(c.cache)
}
