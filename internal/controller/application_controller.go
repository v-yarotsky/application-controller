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
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
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

	EventPodMonitorCreated      = "PodMonitorCreated"
	EventPodMonitorUpdated      = "PodMonitorUpdated"
	EventPodMonitorUpsertFailed = "PodMonitorUpsertFailed"
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
	name := namer.DeploymentName()
	log := log.FromContext(ctx).WithValues("deploymentname", name.String())

	deploy := appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name.Name,
			Namespace: name.Namespace,
		},
	}

	imgRef, err := r.ImageFinder.FindImage(ctx, app.Spec.Image)
	if err != nil {
		log.Error(err, "failed to find the latest version of the image")
		r.Recorder.Eventf(app, corev1.EventTypeWarning, EventImageCheckFailed, "New image version check failed: %s", err.Error())
		return nil, err
	}

	result, err := controllerutil.CreateOrPatch(ctx, r.Client, &deploy, func() error {
		selectorLabels := namer.SelectorLabels()

		deploy.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: selectorLabels,
		}

		deploy.Spec.Template.ObjectMeta = metav1.ObjectMeta{
			Labels: selectorLabels,
		}

		podTemplateSpec := &deploy.Spec.Template.Spec
		podTemplateSpec.ServiceAccountName = namer.ServiceAccountName().Name

		vols := make([]corev1.Volume, 0, len(app.Spec.Volumes))
		for _, v := range app.Spec.Volumes {
			vols = append(vols, v.Volume)
		}
		podTemplateSpec.Volumes = vols

		var container *corev1.Container
		if len(podTemplateSpec.Containers) == 0 {
			podTemplateSpec.Containers = []corev1.Container{{}}
		}
		container = &podTemplateSpec.Containers[0]

		container.Name = app.Name
		if container.Image != imgRef.String() {
			log.Info("Updating container image", "oldimage", container.Image, "newimage", imgRef.String())
			r.Recorder.Eventf(app, corev1.EventTypeNormal, EventImageUpdated, "Updating image %s -> %s", container.Image, imgRef)
			app.Status.Image = imgRef.String()
			app.Status.ImageLastUpdateTime = metav1.Now()
		}
		container.Image = imgRef.String()
		container.ImagePullPolicy = "Always"
		container.Command = app.Spec.Command
		container.Args = app.Spec.Args
		container.Env = app.Spec.Env
		container.Ports = app.Spec.Ports
		container.Resources = app.Spec.Resources
		container.LivenessProbe = app.Spec.LivenessProbe
		container.ReadinessProbe = app.Spec.ReadinessProbe
		container.StartupProbe = app.Spec.StartupProbe
		container.SecurityContext = app.Spec.SecurityContext

		mounts := make([]corev1.VolumeMount, 0, len(app.Spec.Volumes))
		for _, v := range app.Spec.Volumes {
			mounts = append(mounts, corev1.VolumeMount{
				Name:      v.Name,
				MountPath: v.MountPath,
			})
		}
		container.VolumeMounts = mounts

		if err := controllerutil.SetControllerReference(app, &deploy, r.Scheme); err != nil {
			log.Error(err, "failed to set controller reference on Deployment")
			return err
		}
		return nil
	})
	if err != nil {
		log.Error(err, "failed to create/update the Deployment")
		r.Recorder.Eventf(app, corev1.EventTypeWarning, EventDeploymentUpsertFailed, "Could not upsert Deployment %s: %s", name, err)
		return nil, fmt.Errorf("failed up upsert deployment: %w", err)
	}

	switch result {
	case controllerutil.OperationResultCreated:
		log.Info("Created Deployment")
		r.Recorder.Eventf(app, corev1.EventTypeNormal, EventDeploymentCreated, "Deployment %s has been created", name)
	case controllerutil.OperationResultUpdated:
		log.Info("Updated Deployment")
		r.Recorder.Eventf(app, corev1.EventTypeNormal, EventDeploymentUpdated, "Deployment %s has been updated", name)
	}

	return &deploy, nil
}

func (r *ApplicationReconciler) ensureServiceAccount(ctx context.Context, app *yarotskymev1alpha1.Application, namer Namer) error {
	name := namer.ServiceAccountName()
	log := log.FromContext(ctx).WithValues("serviceaccountname", name.String())

	var sa corev1.ServiceAccount
	sa = corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name.Name,
			Namespace: name.Namespace,
		},
	}

	result, err := controllerutil.CreateOrPatch(ctx, r.Client, &sa, func() error {
		if err := controllerutil.SetControllerReference(app, &sa, r.Scheme); err != nil {
			log.Error(err, "failed to set controller reference on ServiceAccount")
			return err
		}
		return nil
	})
	if err != nil {
		log.Error(err, "failed to create/update the ServiceAccount")
		r.Recorder.Eventf(app, corev1.EventTypeWarning, EventServiceAccountUpsertFailed, "Could not upsert service account %s: %s", name, err)
		return fmt.Errorf("failed to upsert ServiceAccount: %w", err)
	}

	switch result {
	case controllerutil.OperationResultCreated:
		log.Info("Created ServiceAccount")
		r.Recorder.Eventf(app, corev1.EventTypeNormal, EventServiceAccountCreated, "ServiceAccount %s has been created", name)
	case controllerutil.OperationResultUpdated:
		log.Info("Updated ServiceAccount")
		r.Recorder.Eventf(app, corev1.EventTypeNormal, EventServiceAccountUpdated, "ServiceAccount %s has been updated", name)
	}
	return nil
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
	name := namer.ServiceName()
	log := log.FromContext(ctx).WithValues("servicename", name.String())

	svc := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name.Name,
			Namespace: name.Namespace,
		},
	}

	ports := make([]corev1.ServicePort, 0, len(app.Spec.Ports))
	for _, p := range app.Spec.Ports {
		ports = append(ports, corev1.ServicePort{
			Name:       p.Name,
			TargetPort: intstr.FromString(p.Name),
			Protocol:   p.Protocol,
			Port:       p.ContainerPort,
		})
	}

	result, err := controllerutil.CreateOrPatch(ctx, r.Client, &svc, func() error {
		svc.Spec.Ports = reconcilePorts(svc.Spec.Ports, ports)
		svc.Spec.Selector = namer.SelectorLabels()
		if err := controllerutil.SetControllerReference(app, &svc, r.Scheme); err != nil {
			log.Error(err, "failed to set controller reference on Service")
			return err
		}
		return nil
	})

	if err != nil {
		log.Error(err, "failed to create/update the Service")
		r.Recorder.Eventf(app, corev1.EventTypeWarning, EventServiceUpsertFailed, "Could not upsert Service %s: %s", name, err)
		return fmt.Errorf("failed to upsert Service %s: %w", name, err)
	}

	switch result {
	case controllerutil.OperationResultCreated:
		log.Info("Created Service")
		r.Recorder.Eventf(app, corev1.EventTypeNormal, EventServiceCreated, "Service %s has been created", name)
	case controllerutil.OperationResultUpdated:
		log.Info("Updated Service")
		r.Recorder.Eventf(app, corev1.EventTypeNormal, EventServiceUpdated, "Service %s has been updated", name)
	}
	return nil
}

func (r *ApplicationReconciler) ensureLBService(ctx context.Context, app *yarotskymev1alpha1.Application, namer Namer) error {
	log := log.FromContext(ctx)
	lb := app.Spec.LoadBalancer

	if lb == nil {
		log.Info("Application does not specify a loadBalancer; skipping.")
		return nil
	}

	name := namer.LBServiceName()
	log = log.WithValues("lbservicename", name.String())

	svc := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name.Name,
			Namespace: name.Namespace,
		},
	}

	appPortsByName := make(map[string]corev1.ContainerPort, len(app.Spec.Ports))
	for _, port := range app.Spec.Ports {
		appPortsByName[port.Name] = port
	}
	ports := make([]corev1.ServicePort, 0, len(lb.PortNames))
	for _, n := range lb.PortNames {
		if p, ok := appPortsByName[n]; !ok {
			return fmt.Errorf("loadBalancer specifies an unknown port name %q", n)
		} else {
			ports = append(ports, corev1.ServicePort{
				Name:       p.Name,
				TargetPort: intstr.FromString(p.Name),
				Protocol:   p.Protocol,
				Port:       p.ContainerPort,
			})
		}
	}

	result, err := controllerutil.CreateOrPatch(ctx, r.Client, &svc, func() error {
		svc.Annotations = addToMap[string, string](svc.Annotations, ExternalDNSHostnameAnnotation, lb.Host)
		svc.Spec.Type = corev1.ServiceTypeLoadBalancer
		svc.Spec.Ports = reconcilePorts(svc.Spec.Ports, ports)
		svc.Spec.Selector = namer.SelectorLabels()
		if err := controllerutil.SetControllerReference(app, &svc, r.Scheme); err != nil {
			log.Error(err, "failed to set controller reference on Service")
			return err
		}
		return nil
	})

	if err != nil {
		log.Error(err, "failed to create/update the LoadBalancerService")
		r.Recorder.Eventf(app, corev1.EventTypeWarning, EventLBServiceUpsertFailed, "Could not upsert LoadBalancerService %s: %s", name, err)
		return fmt.Errorf("failed to upsert LoadBalancerService %s: %w", name, err)
	}

	switch result {
	case controllerutil.OperationResultCreated:
		log.Info("Created LoadBalancerService")
		r.Recorder.Eventf(app, corev1.EventTypeNormal, EventLBServiceCreated, "LoadBalancerService %s has been created", name)
	case controllerutil.OperationResultUpdated:
		log.Info("Updated LoadBalancerService")
		r.Recorder.Eventf(app, corev1.EventTypeNormal, EventLBServiceUpdated, "LoadBalancerService %s has been updated", name)
	}
	return nil
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

	if app.Spec.Metrics == nil {
		log.Info("Monitoring is not configured for the app; skipping.")
		return nil
	}

	name := namer.PodMonitorName()
	log = log.WithValues("servicemonitorname", name.String())

	portName := app.Spec.Metrics.PortName
	if portName == "" {
		for _, p := range app.Spec.Ports {
			if p.Name == "metrics" || p.Name == "prometheus" {
				portName = p.Name
				break
			}
		}
	}

	if portName == "" {
		log.Info(`No monitoring port is specified, and there's no port named "metrics" or "prometheus"; skipping PodMonitor creation`)
		return nil
	}

	path := app.Spec.Metrics.Path
	if path == "" {
		path = "/metrics"
	}

	mon := prometheusv1.PodMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name.Name,
			Namespace: name.Namespace,
		},
	}

	result, err := controllerutil.CreateOrPatch(ctx, r.Client, &mon, func() error {
		mon.Spec.PodMetricsEndpoints = []prometheusv1.PodMetricsEndpoint{
			{
				Port: portName,
				Path: path,
			},
		}
		mon.Spec.Selector = metav1.LabelSelector{MatchLabels: namer.SelectorLabels()}
		mon.Spec.PodTargetLabels = []string{"app.kubernetes.io/name"}

		if err := controllerutil.SetControllerReference(app, &mon, r.Scheme); err != nil {
			log.Error(err, "failed to set controller reference on PodMonitor")
			return err
		}
		return nil
	})

	if err != nil {
		log.Error(err, "failed to create/update the PodMonitor")
		r.Recorder.Eventf(app, corev1.EventTypeWarning, EventPodMonitorUpsertFailed, "Could not upsert PodMonitor %s: %s", name, err)
		return fmt.Errorf("failed to upsert PodMonitor %s: %w", name, err)
	}

	switch result {
	case controllerutil.OperationResultCreated:
		log.Info("Created PodMonitor")
		r.Recorder.Eventf(app, corev1.EventTypeNormal, EventPodMonitorCreated, "PodMonitor %s has been created", name)
	case controllerutil.OperationResultUpdated:
		log.Info("Updated PodMonitor")
		r.Recorder.Eventf(app, corev1.EventTypeNormal, EventPodMonitorUpdated, "PodMonitor %s has been updated", name)
	}
	return nil
}

func (r *ApplicationReconciler) ensureIngress(ctx context.Context, app *yarotskymev1alpha1.Application, namer Namer) error {
	name := namer.IngressName()
	log := log.FromContext(ctx).WithValues("ingressname", name.String())

	var ing networkingv1.Ingress
	found := false
	if err := r.Client.Get(ctx, name, &ing); err != nil {
		if !apierrors.IsNotFound(err) {
			log.Error(err, "failed to fetch Ingress")
			return err
		}
	} else {
		found = true
	}

	if found && !objectIsOwnedBy(&ing, app) {
		err := fmt.Errorf("Ingress %s is not owned by the Application; refusing to reconcile!", name)
		log.Error(err, "failed to reconcile the Ingress")
		return err
	}

	if app.Spec.Ingress != nil {
		if !found || found && objectIsOwnedBy(&ing, app) {
			portName := app.Spec.Ingress.PortName
			if portName == "" {
				for _, p := range app.Spec.Ports {
					if p.Name == "http" {
						portName = "http"
						break
					}
				}
			}
			if portName == "" {
				return fmt.Errorf(`Could not find the port for Ingress. Either specify one explicitly, or ensure there's a port named "http" or "web"`)
			}
			pathType := networkingv1.PathTypeImplementationSpecific

			ingressClassName := app.Spec.Ingress.IngressClassName
			if ingressClassName == nil {
				if r.DefaultIngressClassName == "" {
					return fmt.Errorf("ingress.ingressClassName is not specified, and --ingress-class is not set.")
				}
				ingressClassName = ptr.To(r.DefaultIngressClassName)
			}

			ing.ObjectMeta.Name = name.Name
			ing.ObjectMeta.Namespace = name.Namespace

			result, err := controllerutil.CreateOrPatch(ctx, r.Client, &ing, func() error {
				ing.ObjectMeta.Annotations = r.DefaultIngressAnnotations

				ing.Spec.IngressClassName = ingressClassName
				ing.Spec.Rules = []networkingv1.IngressRule{
					{
						Host: app.Spec.Ingress.Host,
						IngressRuleValue: networkingv1.IngressRuleValue{
							HTTP: &networkingv1.HTTPIngressRuleValue{
								Paths: []networkingv1.HTTPIngressPath{
									{
										Path:     "/",
										PathType: &pathType,
										Backend: networkingv1.IngressBackend{
											Service: &networkingv1.IngressServiceBackend{
												Name: namer.ServiceName().Name,
												Port: networkingv1.ServiceBackendPort{
													Name: portName,
												},
											},
										},
									},
								},
							},
						},
					},
				}
				if err := controllerutil.SetControllerReference(app, &ing, r.Scheme); err != nil {
					log.Error(err, "failed to set controller reference on Ingress")
					return err
				}
				return nil
			})

			if err != nil {
				log.Error(err, "failed to create/update the Ingress")
				r.Recorder.Eventf(app, corev1.EventTypeWarning, EventIngressUpsertFailed, "Could not upsert Ingress %s: %s", name, err)
				return fmt.Errorf("failed to upsert Ingress %s: %w", name, err)
			}

			switch result {
			case controllerutil.OperationResultCreated:
				log.Info("Created Ingress")
				r.Recorder.Eventf(app, corev1.EventTypeNormal, EventIngressCreated, "Ingress %s has been created", name)
			case controllerutil.OperationResultUpdated:
				log.Info("Updated Ingress")
				r.Recorder.Eventf(app, corev1.EventTypeNormal, EventIngressUpdated, "Ingress %s has been updated", name)
			}
		}

	} else {
		if found {
			// delete
			err := r.Client.Delete(ctx, &ing)
			if err != nil {
				err = fmt.Errorf("failed to delete Ingress %s: %w", name, err)
				log.Error(err, "")
				r.Recorder.Eventf(app, corev1.EventTypeWarning, EventIngressUpsertFailed, "%s", err.Error())
				return err
			}
			return nil
		} else {
			log.Info("Application does not specify Ingress, skipping Ingress creation.")
			return nil
		}
	}

	return nil
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
