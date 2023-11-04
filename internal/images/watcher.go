package images

import (
	"context"
	"time"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
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
		interval: 30 * time.Second,
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

	// TODO pagination
	list := &yarotskymev1alpha1.ApplicationList{}
	err := w.client.List(context.Background(), list)
	if err != nil {
		log.Error(err, "failed to get the list of Applications to check for image updates")
		return
	}
	for _, app := range list.Items {
		c <- event.GenericEvent{Object: &app}
	}
}
