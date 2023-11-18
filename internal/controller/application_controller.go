/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/source"

	prometheusv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	"git.home.yarotsky.me/vlad/application-controller/internal/gkutil"
	"git.home.yarotsky.me/vlad/application-controller/internal/images"
	"git.home.yarotsky.me/vlad/application-controller/internal/k8s"
	osdkHandler "github.com/operator-framework/operator-lib/handler"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	Name                          = "application-controller"
	ExternalDNSHostnameAnnotation = "external-dns.alpha.kubernetes.io/hostname"

	EventClusterRoleBindingCreated      = "ClusterRoleBindingCreated"
	EventClusterRoleBindingUpdated      = "ClusterRoleBindingUpdated"
	EventClusterRoleBindingUpsertFailed = "ClusterRoleBindingUpsertFailed"
	EventDeleteClusterRoleBindingFailed = "DeleteClusterRoleBindingFailed"
	EventDeletingClusterRoleBinding     = "DeletingClusterRoleBinding"

	EventDeploymentCreated      = "DeploymentCreated"
	EventDeploymentUpdated      = "DeploymentUpdated"
	EventDeploymentUpsertFailed = "DeploymentUpsertFailed"

	EventImageCheckFailed = "ImageCheckFailed"
	EventImageUpdated     = "ImageUpdated"

	EventIngressCreated      = "IngressCreated"
	EventIngressUpdated      = "IngressUpdated"
	EventIngressUpsertFailed = "IngressUpsertFailed"
	EventIngressDeleted      = "IngressDeleted"
	EventIngressDeleteFailed = "IngressDeleteFailed"

	EventReasonCleanup = "Cleanup"

	EventRoleBindingCreated      = "RoleBindingCreated"
	EventRoleBindingUpdated      = "RoleBindingUpdated"
	EventRoleBindingUpsertFailed = "RoleBindingUpsertFailed"

	EventServiceAccountCreated      = "ServiceAccountCreated"
	EventServiceAccountUpdated      = "ServiceAccountUpdated"
	EventServiceAccountUpsertFailed = "ServiceAccountUpsertFailed"

	EventServiceCreated      = "ServiceCreated"
	EventServiceUpdated      = "ServiceUpdated"
	EventServiceUpsertFailed = "ServiceUpsertFailed"

	EventLBServiceCreated      = "LoadBalancerServiceCreated"
	EventLBServiceUpdated      = "LoadBalancerServiceUpdated"
	EventLBServiceUpsertFailed = "LoadBalancerServiceUpsertFailed"
	EventLBServiceDeleted      = "LoadBalancerServiceDeleted"
	EventLBServiceDeleteFailed = "LoadBalancerServiceDeleteFailed"

	EventPodMonitorCreated      = "PodMonitorCreated"
	EventPodMonitorUpdated      = "PodMonitorUpdated"
	EventPodMonitorUpsertFailed = "PodMonitorUpsertFailed"
	EventPodMonitorDeleted      = "PodMonitorDeleted"
	EventPodMonitorDeleteFailed = "PodMonitorDeleteFailed"
)

// ApplicationReconciler reconciles a Application object
type ApplicationReconciler struct {
	ImageFinder               images.ImageFinder
	ImageUpdateEvents         chan event.GenericEvent
	DefaultIngressClassName   string
	DefaultIngressAnnotations map[string]string
	client.Client
	Scheme             *runtime.Scheme
	Recorder           record.EventRecorder
	SupportsPrometheus bool
}

const FinalizerName = "application.yarotsky.me/finalizer"

// This is necessary to be able to query a list of RoleBinding objects owned by an Application
const roleBindingOwnerKey = "metadata.controller"

//+kubebuilder:rbac:groups=yarotsky.me,resources=applications,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=yarotsky.me,resources=applications/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=yarotsky.me,resources=applications/finalizers,verbs=update
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps,resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterrolebindings,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles,verbs=bind
//+kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles,verbs=bind
//+kubebuilder:rbac:groups=core,resources=events,verbs=create;patch
//+kubebuilder:rbac:groups=monitoring.coreos.com,resources=podmonitors,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.16.0/pkg/reconcile
func (r *ApplicationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	var app yarotskymev1alpha1.Application
	if err := r.Get(ctx, req.NamespacedName, &app); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		log.Error(err, "unable to fetch Application")
		return ctrl.Result{}, err
	}

	namer := &simpleNamer{&app}

	if len(app.Status.Conditions) == 0 {
		r.setConditions(&app, metav1.Condition{Type: yarotskymev1alpha1.ConditionReady, Status: metav1.ConditionUnknown, Reason: "Reconciling", Message: "Starting reconciliation"})
		return r.updateStatus(ctx, &app)
	}

	if app.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(&app, FinalizerName) {
			controllerutil.AddFinalizer(&app, FinalizerName)
			if err := r.Update(ctx, &app); err != nil {
				log.Error(err, "failed to add finalizer")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
	} else {
		if controllerutil.ContainsFinalizer(&app, FinalizerName) {
			// Clean up ClusterRoleBinding objects, if any; these are
			// not deleted via owner references, since cluster-scoped
			// resources cannot be owned by namespaced resources.
			log.Info("Cleaning up ClusterRoleBindings")
			r.Recorder.Eventf(&app, corev1.EventTypeNormal, EventReasonCleanup, "Removing ClusterRoleBindings, if any")

			err := r.ensureNoClusterRoleBindings(ctx, &app, namer)
			if err != nil {
				log.Error(err, "failed to clean up ClusterRoleBindings")
				return ctrl.Result{}, err
			}

			log.Info("Removing finalizer")
			controllerutil.RemoveFinalizer(&app, FinalizerName)
			if err := r.Update(ctx, &app); err != nil {
				log.Error(err, "failed to remove finalizer")
				return ctrl.Result{}, err
			}
		}
		// we are in the process of deletion
		return ctrl.Result{}, nil
	}

	if err := r.ensureServiceAccount(ctx, &app, namer); err != nil {
		return r.updateStatusWithError(ctx, &app, err, EventServiceAccountUpsertFailed)
	}

	if err := r.ensureRoleBindings(ctx, &app, namer); err != nil {
		return r.updateStatusWithError(ctx, &app, err, EventRoleBindingUpsertFailed)
	}

	if err := r.ensureClusterRoleBindings(ctx, &app, namer); err != nil {
		return r.updateStatusWithError(ctx, &app, err, EventClusterRoleBindingUpsertFailed)
	}

	if err := r.ensureService(ctx, &app, namer); err != nil {
		return r.updateStatusWithError(ctx, &app, err, EventServiceUpsertFailed)
	}

	if err := r.ensureLBService(ctx, &app, namer); err != nil {
		return r.updateStatusWithError(ctx, &app, err, EventLBServiceUpsertFailed)
	}

	if err := r.ensurePodMonitor(ctx, &app, namer); err != nil {
		return r.updateStatusWithError(ctx, &app, err, EventPodMonitorUpsertFailed)
	}

	if err := r.ensureIngress(ctx, &app, namer); err != nil {
		return r.updateStatusWithError(ctx, &app, err, EventIngressUpsertFailed)
	}

	if deploy, err := r.ensureDeployment(ctx, &app, namer); err != nil {
		return r.updateStatusWithError(ctx, &app, err, EventDeploymentUpsertFailed)
	} else {
		deployConditions := k8s.ConvertDeploymentConditionsToStandardForm(deploy.Status.Conditions)

		if c := meta.FindStatusCondition(deployConditions, string(appsv1.DeploymentAvailable)); c != nil {
			r.setConditions(&app, metav1.Condition{Type: yarotskymev1alpha1.ConditionReady, Status: c.Status, Reason: c.Reason, Message: c.Message})
		}

		result, err := r.updateStatus(ctx, &app)
		if err == nil {
			log.Info("Application is up to date.")
		}
		return result, err
	}
}

func (r *ApplicationReconciler) ensureDeployment(ctx context.Context, app *yarotskymev1alpha1.Application, namer Namer) (*appsv1.Deployment, error) {
	log := log.FromContext(ctx)

	var deploy appsv1.Deployment
	mutator := &deploymentMutator{
		namer:       namer,
		imageFinder: r.ImageFinder,
	}

	err := r.ensureResource(
		ctx,
		app,
		namer,
		namer.DeploymentName(),
		&deploy,
		mutator.Mutate(ctx, app, &deploy),
		true,
		eventMap{
			Created:      EventDeploymentCreated,
			Updated:      EventDeploymentUpdated,
			UpsertFailed: EventDeploymentUpsertFailed,
		},
	)

	if err != nil {
		if errors.Is(err, ErrImageCheckFailed) {
			log.Error(err, "")
			r.Recorder.Eventf(app, corev1.EventTypeWarning, EventImageCheckFailed, "New image version check failed: %s", err.Error())
		}
		return nil, err
	}

	oldImage := app.Status.Image
	newImage := deploy.Spec.Template.Spec.Containers[0].Image
	if oldImage != newImage {
		log.Info("Updated container image", "oldimage", oldImage, "newimage", newImage)
		r.Recorder.Eventf(app, corev1.EventTypeNormal, EventImageUpdated, "Updating image %s -> %s", oldImage, newImage)

		app.Status.Image = newImage
		app.Status.ImageLastUpdateTime = metav1.Now()
	}

	return &deploy, nil
}

func (r *ApplicationReconciler) ensureServiceAccount(ctx context.Context, app *yarotskymev1alpha1.Application, namer Namer) error {
	var sa corev1.ServiceAccount

	return r.ensureResource(
		ctx,
		app,
		namer,
		namer.ServiceAccountName(),
		&sa,
		func() error { return nil },
		true,
		eventMap{
			Created:      EventServiceAccountCreated,
			Updated:      EventServiceAccountUpdated,
			UpsertFailed: EventServiceAccountUpsertFailed,
		},
	)
}

func (r *ApplicationReconciler) ensureRoleBindings(ctx context.Context, app *yarotskymev1alpha1.Application, namer Namer) error {
	baseLog := log.FromContext(ctx)

	serviceAccountName := namer.ServiceAccountName()

	rbs := &rbacv1.RoleBindingList{}

	err := r.Client.List(ctx, rbs, client.InNamespace(app.Namespace), client.MatchingFields(map[string]string{roleBindingOwnerKey: app.Name}))
	if err != nil {
		baseLog.Error(err, "failed to get the list of owned RoleBinding objects")
		return err
	}

	seenSet := make(map[string]bool, len(rbs.Items))
	for _, rb := range rbs.Items {
		seenSet[rb.Name] = false
	}

	for _, roleRef := range app.Spec.Roles {
		log := baseLog.WithValues("kind", roleRef.Kind, "name", roleRef.Name)

		name, err := namer.RoleBindingName(roleRef.RoleRef)
		if err != nil {
			log.Error(err, "failed to generate name for a RoleBinding")
			return err
		}

		log = log.WithValues("rbname", name.Name)

		if roleRef.ScopeOrDefault() != yarotskymev1alpha1.RoleBindingScopeNamespace {
			continue
		}
		rb := rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name.Name,
				Namespace: name.Namespace,
			},
		}
		result, err := controllerutil.CreateOrPatch(ctx, r.Client, &rb, func() error {
			rb.Subjects = []rbacv1.Subject{
				{
					APIGroup:  "",
					Kind:      "ServiceAccount",
					Name:      serviceAccountName.Name,
					Namespace: serviceAccountName.Namespace,
				},
			}
			rb.RoleRef = roleRef.RoleRef
			if err := controllerutil.SetControllerReference(app, &rb, r.Scheme); err != nil {
				log.Error(err, "failed to set controller reference on RoleBinding")
				return err
			}

			return nil
		})
		if err != nil {
			log.Error(err, "failed to create/update RoleBinding")
			r.Recorder.Eventf(app, corev1.EventTypeWarning, EventRoleBindingUpsertFailed, "Could not upsert RoleBinding %s: %s", name, err)
			return fmt.Errorf("failed to upsert RoleBinding %s: %w", name, err)
		}

		switch result {
		case controllerutil.OperationResultCreated:
			log.Info("RoleBinding created")
			r.Recorder.Eventf(app, corev1.EventTypeNormal, EventRoleBindingCreated, "RoleBinding %s has been created", name)
		case controllerutil.OperationResultUpdated:
			log.Info("RoleBinding updated")
			r.Recorder.Eventf(app, corev1.EventTypeNormal, EventRoleBindingUpdated, "RoleBinding %s has been updated", name)
		}
		seenSet[name.Name] = true
	}

	for rbName, seen := range seenSet {
		if seen {
			continue
		}
		log := baseLog.WithValues("name", rbName)
		log.Info("RoleBinding is not needed according to spec. Deleting.")
		rb := rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      rbName,
				Namespace: app.Namespace,
			},
		}
		err := r.Client.Delete(ctx, &rb)
		if err != nil {
			log.Error(err, "failed to delete RoleBinding")
			return err
		}
	}

	return nil
}

func (r *ApplicationReconciler) ensureClusterRoleBindings(ctx context.Context, app *yarotskymev1alpha1.Application, namer Namer) error {
	baseLog := log.FromContext(ctx)

	serviceAccountName := namer.ServiceAccountName()

	crbs, err := r.getOwnedClusterRoleBindings(ctx, namer)
	if err != nil {
		return err
	}

	seenSet := make(map[string]bool, len(crbs))
	for _, crb := range crbs {
		seenSet[crb.Name] = false
	}

	for _, roleRef := range app.Spec.Roles {
		log := baseLog.WithValues("kind", roleRef.Kind, "name", roleRef.Name)

		if roleRef.ScopeOrDefault() != yarotskymev1alpha1.RoleBindingScopeCluster {
			continue
		}

		name, err := namer.ClusterRoleBindingName(roleRef.RoleRef)
		if err != nil {
			baseLog.Error(err, "failed to generate name for a ClusterRoleBinding")
			return err
		}

		log = log.WithValues("crbname", name.Name)

		if !gkutil.IsClusterRole(gkutil.FromRoleRef(roleRef.RoleRef)) {
			log.Error(err, "ClusterRoleBinding can only be created for a ClusterRole")
			return err
		}
		rb := rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: name.Name},
		}
		result, err := controllerutil.CreateOrPatch(ctx, r.Client, &rb, func() error {
			osdkHandler.SetOwnerAnnotations(app, &rb)

			rb.Subjects = []rbacv1.Subject{
				{
					APIGroup:  "",
					Kind:      "ServiceAccount",
					Name:      serviceAccountName.Name,
					Namespace: serviceAccountName.Namespace,
				},
			}
			rb.RoleRef = roleRef.RoleRef

			return nil
		})
		if err != nil {
			log.Error(err, "failed to create/update a ClusterRoleBinding")
			r.Recorder.Eventf(app, corev1.EventTypeWarning, EventClusterRoleBindingUpsertFailed, "Could not upsert ClusterRoleBinding %s: %s", name, err)
			return fmt.Errorf("failed to upsert ClusterRoleBinding %s: %w", name, err)
		}

		switch result {
		case controllerutil.OperationResultCreated:
			log.Info("ClusterRoleBinding created")
			r.Recorder.Eventf(app, corev1.EventTypeNormal, EventClusterRoleBindingCreated, "ClusterRoleBinding %s has been created", name)
		case controllerutil.OperationResultUpdated:
			log.Info("ClusterRoleBinding updated")
			r.Recorder.Eventf(app, corev1.EventTypeNormal, EventClusterRoleBindingUpdated, "ClusterRoleBinding %s has been updated", name)
		}
		seenSet[name.Name] = true
	}

	for crbName, seen := range seenSet {
		if seen {
			continue
		}
		log := baseLog.WithValues("name", crbName)
		log.Info("ClusterRoleBinding is not needed according to spec. Deleting.")
		crb := rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: crbName},
		}
		err := r.Client.Delete(ctx, &crb)
		if err != nil {
			log.Error(err, "failed to delete ClusterRoleBinding")
			return err
		}
	}

	return nil
}

func (r *ApplicationReconciler) ensureService(ctx context.Context, app *yarotskymev1alpha1.Application, namer Namer) error {
	var svc corev1.Service
	mutator := &serviceMutator{
		namer: namer,
	}

	return r.ensureResource(
		ctx,
		app,
		namer,
		namer.ServiceName(),
		&svc,
		mutator.Mutate(ctx, app, &svc),
		true,
		eventMap{
			Created:      EventIngressCreated,
			Updated:      EventIngressUpdated,
			UpsertFailed: EventIngressUpsertFailed,
		},
	)
}

func (r *ApplicationReconciler) ensureLBService(ctx context.Context, app *yarotskymev1alpha1.Application, namer Namer) error {
	var svc corev1.Service
	mutator := &lbServiceMutator{
		namer: namer,
	}

	return r.ensureResource(
		ctx,
		app,
		namer,
		namer.LBServiceName(),
		&svc,
		mutator.Mutate(ctx, app, &svc),
		app.Spec.LoadBalancer != nil,
		eventMap{
			Created:      EventLBServiceCreated,
			Updated:      EventLBServiceUpdated,
			UpsertFailed: EventLBServiceUpsertFailed,
			Deleted:      EventLBServiceDeleted,
			DeleteFailed: EventLBServiceDeleteFailed,
		},
	)
}

func reconcilePorts(actual []corev1.ServicePort, desired []corev1.ServicePort) []corev1.ServicePort {
	if len(actual) == 0 {
		return desired
	}

	desiredPortsByName := make(map[string]corev1.ServicePort, len(desired))
	seen := make(map[string]bool, len(desired))

	for _, p := range desired {
		desiredPortsByName[p.Name] = p
	}

	result := make([]corev1.ServicePort, 0, len(desired))
	for _, got := range actual {
		if want, ok := desiredPortsByName[got.Name]; ok {
			got.TargetPort = want.TargetPort
			got.Protocol = want.Protocol
			result = append(result, got)
			seen[got.Name] = true
		}
	}

	for _, want := range desired {
		if seen[want.Name] {
			continue
		}
		result = append(result, want)
	}

	return result
}

func (r *ApplicationReconciler) ensurePodMonitor(ctx context.Context, app *yarotskymev1alpha1.Application, namer Namer) error {
	log := log.FromContext(ctx)

	if !r.SupportsPrometheus {
		log.Info("Cluster does not appear to support Prometheus monitoring; skipping.")
		return nil
	}

	var mon prometheusv1.PodMonitor

	mutator := &podMonitorMutator{
		namer: namer,
	}

	return r.ensureResource(
		ctx,
		app,
		namer,
		namer.PodMonitorName(),
		&mon,
		mutator.Mutate(ctx, app, &mon),
		app.Spec.Metrics != nil && app.Spec.Metrics.Enabled,
		eventMap{
			Created:      EventPodMonitorCreated,
			Updated:      EventPodMonitorUpdated,
			UpsertFailed: EventPodMonitorUpsertFailed,
			Deleted:      EventPodMonitorDeleted,
			DeleteFailed: EventPodMonitorDeleteFailed,
		},
	)
}

type eventMap struct {
	Created      string
	Updated      string
	UpsertFailed string
	Deleted      string
	DeleteFailed string
}

func (r *ApplicationReconciler) ensureResource(
	ctx context.Context,
	app *yarotskymev1alpha1.Application,
	namer Namer,
	name types.NamespacedName,
	obj client.Object,
	mutateFn func() error,
	wanted bool,
	events eventMap,
) error {
	gvk, err := apiutil.GVKForObject(obj, r.Scheme)
	if err != nil {
		return err
	}

	log := log.FromContext(ctx).WithValues("GVK", gvk, "name", name)

	found := false
	if err := r.Client.Get(ctx, name, obj); err != nil {
		if !apierrors.IsNotFound(err) {
			log.Error(err, "fetch failed")
			return err
		}
	} else {
		found = true
	}

	if found && !objectIsOwnedBy(obj, app) {
		err := fmt.Errorf("%s %s is not owned by the Application; refusing to reconcile!", gvk, name)
		log.Error(err, "")
		return err
	}

	if wanted {
		if !found || found && objectIsOwnedBy(obj, app) {
			obj.SetName(name.Name)
			obj.SetNamespace(name.Namespace)

			result, err := controllerutil.CreateOrPatch(ctx, r.Client, obj, func() error {
				if err := mutateFn(); err != nil {
					return err
				}
				if err := controllerutil.SetControllerReference(app, obj, r.Scheme); err != nil {
					log.Error(err, "failed to set controller reference")
					return err
				}
				return nil
			})

			if err != nil {
				err = fmt.Errorf("failed to upsert %s %s: %w", gvk, name, err)
				log.Error(err, "")
				r.Recorder.Eventf(app, corev1.EventTypeWarning, events.UpsertFailed, err.Error())
				return err
			}

			switch result {
			case controllerutil.OperationResultCreated:
				log.Info("Created")
				r.Recorder.Eventf(app, corev1.EventTypeNormal, events.Created, "%s %s has been created", gvk, name)
			case controllerutil.OperationResultUpdated:
				log.Info("Updated")
				r.Recorder.Eventf(app, corev1.EventTypeNormal, events.Updated, "%s %s has been updated", gvk, name)
			}
		}

	} else {
		if found {
			err := r.Client.Delete(ctx, obj)
			if err != nil {
				err = fmt.Errorf("failed to delete %s %s: %w", gvk, name, err)
				log.Error(err, "")
				r.Recorder.Eventf(app, corev1.EventTypeWarning, events.DeleteFailed, err.Error())
				return err
			}
			return nil
		} else {
			log.Info("Application does not need the resource; skipping creation.")
			return nil
		}
	}

	return nil
}

func (r *ApplicationReconciler) ensureIngress(ctx context.Context, app *yarotskymev1alpha1.Application, namer Namer) error {
	var ing networkingv1.Ingress
	mutator := &ingressMutator{
		DefaultIngressAnnotations: r.DefaultIngressAnnotations,
		DefaultIngressClassName:   r.DefaultIngressClassName,
		namer:                     namer,
	}

	return r.ensureResource(
		ctx,
		app,
		namer,
		namer.IngressName(),
		&ing,
		mutator.Mutate(ctx, app, &ing),
		app.Spec.Ingress != nil,
		eventMap{
			Created:      EventIngressCreated,
			Updated:      EventIngressUpdated,
			UpsertFailed: EventIngressUpsertFailed,
			Deleted:      EventIngressDeleted,
			DeleteFailed: EventIngressDeleteFailed,
		},
	)
}

func (r *ApplicationReconciler) ensureNoClusterRoleBindings(ctx context.Context, app *yarotskymev1alpha1.Application, namer Namer) error {
	log := log.FromContext(ctx)

	ownedClusterRoleBindings, err := r.getOwnedClusterRoleBindings(ctx, namer)
	if err != nil {
		return err
	}

	if len(ownedClusterRoleBindings) == 0 {
		return nil
	}

	for _, crb := range ownedClusterRoleBindings {
		log = log.WithValues("kind", crb.Kind, "name", crb.Name)
		log.Info("Deleting ClusterRoleBinding")
		r.Recorder.Eventf(app, corev1.EventTypeNormal, EventDeletingClusterRoleBinding, "Deleting ClusterRoleBinding %s", crb.Name)
		err = r.Delete(ctx, &crb)
		if err != nil {
			log.Error(err, "failed to delete ClusterRoleBinding")
			r.Recorder.Eventf(app, corev1.EventTypeWarning, EventDeleteClusterRoleBindingFailed, "Could not delete ClusterRoleBinding %s: %s", crb.Name, err.Error())
			return err
		}
	}

	return nil
}

func (r *ApplicationReconciler) getOwnedClusterRoleBindings(ctx context.Context, namer Namer) ([]rbacv1.ClusterRoleBinding, error) {
	log := log.FromContext(ctx)

	ownedClusterRoleBindings := &rbacv1.ClusterRoleBindingList{}
	err := r.Client.List(ctx, ownedClusterRoleBindings, client.MatchingFields(map[string]string{roleBindingOwnerKey: namer.ApplicationName().String()}))
	if err != nil {
		log.Error(err, "failed to get the list of owned ClusterRoleBinding objects")
		return nil, err
	}
	return ownedClusterRoleBindings.Items, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ApplicationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	indexer := mgr.GetFieldIndexer()
	err := r.setupIndexes(indexer)
	if err != nil {
		return err
	}

	builder := ctrl.NewControllerManagedBy(mgr).
		For(&yarotskymev1alpha1.Application{})

	builder.
		Owns(&appsv1.Deployment{}).                                                                    // Reconcile when an owned Deployment is changed.
		Owns(&corev1.Service{}).                                                                       // Reconcile when an owned Service is changed.
		Owns(&corev1.ServiceAccount{}).                                                                // Reconcile when an owned AccountService is changed.
		Owns(&networkingv1.Ingress{}).                                                                 // Reconcile when an owned Ingress is changed.
		Owns(&rbacv1.RoleBinding{}).                                                                   // Reconcile when an owned RoleBinding is changed.
		Watches(&rbacv1.ClusterRoleBinding{}, &osdkHandler.EnqueueRequestForAnnotation{Type: r.gk()}). // Reconcile when an "owner" ClusterRoleBinding is changed		WatchesRawSource(
		WatchesRawSource(                                                                              // Reconcile when a new image becomes available for an Application
			&source.Channel{Source: r.ImageUpdateEvents},
			&handler.EnqueueRequestForObject{},
		)

	if r.SupportsPrometheus {
		builder = builder.Owns(&prometheusv1.PodMonitor{}) // Reconcile when an owned PodMonitor is changed
	}

	return builder.Complete(r)
}

func (r *ApplicationReconciler) setupIndexes(indexer client.FieldIndexer) error {
	if err := r.setupRoleBindingIndex(indexer); err != nil {
		return err
	}

	if err := r.setupClusterRoleBindingIndex(indexer); err != nil {
		return err
	}

	return nil
}

// Index owned RoleBinding objects by owner in our cache
func (r *ApplicationReconciler) setupRoleBindingIndex(indexer client.FieldIndexer) error {
	return indexer.IndexField(context.Background(), &rbacv1.RoleBinding{}, roleBindingOwnerKey, func(rawObj client.Object) []string {
		rb := rawObj.(*rbacv1.RoleBinding)
		owner := metav1.GetControllerOf(rb)
		if !gkutil.IsApplication(gkutil.FromOwnerReference(owner)) {
			return nil
		}

		return []string{string(owner.Name)}
	})
}

// Index "owned" ClusterRoleBinding objects by owner in our cache
func (r *ApplicationReconciler) setupClusterRoleBindingIndex(indexer client.FieldIndexer) error {
	return indexer.IndexField(context.Background(), &rbacv1.ClusterRoleBinding{}, roleBindingOwnerKey, func(rawObj client.Object) []string {
		rb := rawObj.(*rbacv1.ClusterRoleBinding)
		annotations := rb.GetAnnotations()

		ownerGroupKind := schema.ParseGroupKind(annotations[osdkHandler.TypeAnnotation])
		if !gkutil.IsApplication(ownerGroupKind) {
			return nil
		}

		if ownerNamespacedName, ok := annotations[osdkHandler.NamespacedNameAnnotation]; ok {
			return []string{ownerNamespacedName}
		}
		return nil
	})
}

func (r *ApplicationReconciler) gk() schema.GroupKind {
	gvk, _ := apiutil.GVKForObject(&yarotskymev1alpha1.Application{}, r.Scheme)
	return gvk.GroupKind()
}

func (r *ApplicationReconciler) setErrorCondition(app *yarotskymev1alpha1.Application, err error, reason string) {
	r.setConditions(app, metav1.Condition{
		Type:    yarotskymev1alpha1.ConditionReady,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: err.Error(),
	})
}

func (r *ApplicationReconciler) setConditions(app *yarotskymev1alpha1.Application, conditions ...metav1.Condition) {
	for _, c := range conditions {
		meta.SetStatusCondition(&app.Status.Conditions, c)
	}
}

func (r *ApplicationReconciler) updateStatus(ctx context.Context, app *yarotskymev1alpha1.Application) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	log.Info("Updating status", "status", app.Status)

	if err := r.Status().Update(ctx, app); err != nil {
		if apierrors.IsConflict(err) {
			log.Info("Got a status update conflict, requeuing.")
			return ctrl.Result{Requeue: true}, nil
		}
		log.Error(err, "failed to update Application status")
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *ApplicationReconciler) updateStatusWithError(ctx context.Context, app *yarotskymev1alpha1.Application, err error, reason string) (ctrl.Result, error) {
	r.setConditions(app, metav1.Condition{
		Type:    yarotskymev1alpha1.ConditionReady,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: err.Error(),
	})
	result, statusErr := r.updateStatus(ctx, app)
	return result, errors.Join(err, statusErr)
}

func addToMap[K comparable, V any](m map[K]V, key K, value V) map[K]V {
	if m == nil {
		m = make(map[K]V)
	}
	m[key] = value
	return m
}

func objectIsOwnedBy(obj client.Object, owner client.Object) bool {
	controller := metav1.GetControllerOf(obj)
	if controller == nil {
		return false
	}

	controllerGV, err := schema.ParseGroupVersion(controller.APIVersion)
	if err != nil {
		return false
	}

	if owner.GetObjectKind() == schema.EmptyObjectKind {
		return false
	}
	return controllerGV.Group == owner.GetObjectKind().GroupVersionKind().Group && controller.Name == owner.GetName()
}
