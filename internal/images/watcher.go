package images

import (
	"context"
	"time"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
