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
	"fmt"

	"go.uber.org/multierr"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/source"

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

// ApplicationReconciler reconciles a Application object
type ApplicationReconciler struct {
	ImageFinder               images.ImageFinder
	ImageUpdateEvents         chan event.GenericEvent
	DefaultIngressClassName   string
	DefaultIngressAnnotations map[string]string
	client.Client
	Scheme *runtime.Scheme
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

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.16.0/pkg/reconcile
func (r *ApplicationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	var app yarotskymev1alpha1.Application
	if err := r.Get(ctx, req.NamespacedName, &app); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		log.Error(err, "unable to fetch Application")
		return ctrl.Result{}, err
	}

	namer := &simpleNamer{&app}

	if len(app.Status.Conditions) == 0 {
		r.setConditions(ctx, &app,
			metav1.Condition{Type: yarotskymev1alpha1.ConditionReady, Status: metav1.ConditionUnknown, Reason: "Reconciling", Message: "Starting reconciliation"},
		)
	}

	if app.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(&app, FinalizerName) {
			controllerutil.AddFinalizer(&app, FinalizerName)
			if err := r.Update(ctx, &app); err != nil {
				return ctrl.Result{}, err
			}
		}

	} else {
		if controllerutil.ContainsFinalizer(&app, FinalizerName) {
			// Clean up ClusterRoleBinding objects, if any; these are
			// not deleted via owner references, since cluster-scoped
			// resources cannot be owned by namespaced resources.
			log.Info("Cleaning up ClusterRoleBindings")
			done, err := r.ensureNoClusterRoleBindings(ctx, &app, namer)
			if err != nil {
				log.Error(err, "failed to clean up ClusterRoleBindings")
				return ctrl.Result{}, err
			}

			// Only remove finalizer after all the ClusterRoleBinding objects have been removed
			// to avoid a racing reconcile calls triggered by deleted ClusterRoleBindings.
			if done {
				log.Info("Removing finalizer")
				controllerutil.RemoveFinalizer(&app, FinalizerName)
				if err := r.Update(ctx, &app); err != nil {
					log.Error(err, "failed to remove finalizer")
					return ctrl.Result{}, err
				}
			}
		}
		// we are in the process of deletion
		return ctrl.Result{}, nil
	}

	if err := r.ensureServiceAccount(ctx, &app, namer); err != nil {
		log.Error(err, "failed to ensure a ServiceAccount exists for the Application")
		return ctrl.Result{}, err
	}

	if err := r.ensureRoleBindings(ctx, &app, namer); err != nil {
		log.Error(err, "failed to ensure a RoleBindings exists for the Application")
		return ctrl.Result{}, err
	}

	if err := r.ensureClusterRoleBindings(ctx, &app, namer); err != nil {
		log.Error(err, "failed to ensure a ClusterRoleBindings exists for the Application")
		return ctrl.Result{}, err
	}

	if err := r.ensureService(ctx, &app, namer); err != nil {
		log.Error(err, "failed to ensure a Service exists for the Application")
		return ctrl.Result{}, err
	}

	if err := r.ensureIngress(ctx, &app, namer); err != nil {
		log.Error(err, "failed to ensure a Ingress exists for the Application")
		return ctrl.Result{}, err
	}

	if deploy, err := r.ensureDeployment(ctx, &app, namer); err != nil {
		log.Error(err, "failed to ensure a Deployment exists for the Application")
		return ctrl.Result{}, err
	} else {
		conditions := []metav1.Condition{}
		deployConditions := k8s.ConvertDeploymentConditionsToStandardForm(deploy.Status.Conditions)

		if c := meta.FindStatusCondition(deployConditions, string(appsv1.DeploymentAvailable)); c != nil {
			meta.SetStatusCondition(&conditions, metav1.Condition{Type: yarotskymev1alpha1.ConditionReady, Status: c.Status, Reason: c.Reason, Message: c.Message})
		}

		err := r.setConditions(ctx, &app, conditions...)
		if err == nil {
			log.Info("Application is up to date.")
		}
		return ctrl.Result{}, err
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
		if container.Image != "" && container.Image != imgRef {
			log.Info("Updating container image", "oldimage", container.Image, "newimage", imgRef)
		}
		container.Image = imgRef
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
		statusErr := r.setConditions(ctx, app, metav1.Condition{
			Type:    yarotskymev1alpha1.ConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "UpsertDeploymentFailed",
			Message: fmt.Sprintf("Failed to upsert a Deployment: %s", err.Error()),
		})
		return nil, multierr.Combine(err, statusErr)
	}

	switch result {
	case controllerutil.OperationResultCreated:
		log.Info("Created Deployment")
	case controllerutil.OperationResultUpdated:
		log.Info("Updated Deployment")
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
		log.Error(err, "failed to create/update the ServiceACcount")
		statusErr := r.setConditions(ctx, app, metav1.Condition{
			Type:    yarotskymev1alpha1.ConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "UpsertServiceAccountFailed",
			Message: fmt.Sprintf("Failed to upsert a ServiceAccount: %s", err.Error()),
		})
		return multierr.Combine(err, statusErr)
	}

	switch result {
	case controllerutil.OperationResultCreated:
		log.Info("Created ServiceAccount")
	case controllerutil.OperationResultUpdated:
		log.Info("Updated ServiceAccount")
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
			statusErr := r.setConditions(ctx, app, metav1.Condition{
				Type:    yarotskymev1alpha1.ConditionReady,
				Status:  metav1.ConditionFalse,
				Reason:  "UpsertRoleBindingError",
				Message: fmt.Sprintf("Failed to upsert a RoleBinding: %s", err.Error()),
			})
			return multierr.Combine(err, statusErr)
		}

		switch result {
		case controllerutil.OperationResultCreated:
			log.Info("RoleBinding created")
		case controllerutil.OperationResultUpdated:
			log.Info("RoleBinding updated")
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
			statusErr := r.setConditions(ctx, app, metav1.Condition{
				Type:    yarotskymev1alpha1.ConditionReady,
				Status:  metav1.ConditionFalse,
				Reason:  "UpsertClusterRoleBindingError",
				Message: fmt.Sprintf("Failed to upsert a ClusterRoleBinding: %s", err.Error()),
			})
			return multierr.Combine(err, statusErr)
		}

		switch result {
		case controllerutil.OperationResultCreated:
			log.Info("ClusterRoleBinding created")
		case controllerutil.OperationResultUpdated:
			log.Info("ClusterRoleBinding updated")
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

	result, err := controllerutil.CreateOrPatch(ctx, r.Client, &svc, func() error {
		ports := make([]corev1.ServicePort, 0, len(app.Spec.Ports))
		for _, p := range app.Spec.Ports {
			ports = append(ports, corev1.ServicePort{
				Name:       p.Name,
				TargetPort: intstr.FromString(p.Name),
				Protocol:   p.Protocol,
				Port:       p.ContainerPort,
			})
		}
		svc.Spec.Ports = ports
		svc.Spec.Selector = namer.SelectorLabels()
		if err := controllerutil.SetControllerReference(app, &svc, r.Scheme); err != nil {
			log.Error(err, "failed to set controller reference on Service")
			return err
		}
		return nil
	})

	if err != nil {
		log.Error(err, "failed to create/update the Service")
		statusErr := r.setConditions(ctx, app, metav1.Condition{
			Type:    yarotskymev1alpha1.ConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "UpsertServiceError",
			Message: fmt.Sprintf("Failed to upsert a Service: %s", err.Error()),
		})
		return multierr.Combine(err, statusErr)
	}

	switch result {
	case controllerutil.OperationResultCreated:
		log.Info("Created Service")
	case controllerutil.OperationResultUpdated:
		log.Info("Updated Service")
	}
	return nil
}

func (r *ApplicationReconciler) ensureIngress(ctx context.Context, app *yarotskymev1alpha1.Application, namer Namer) error {
	name := namer.IngressName()
	log := log.FromContext(ctx).WithValues("ingressname", name.String())

	if app.Spec.Ingress == nil {
		log.Info("Application does not specify Ingress, skipping Ingress creation.")
		return nil
	}

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
		ingressClassName = pointer.String(r.DefaultIngressClassName)
	}

	ing := networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name.Name,
			Namespace: name.Namespace,
		},
	}

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
		statusErr := r.setConditions(ctx, app, metav1.Condition{
			Type:    yarotskymev1alpha1.ConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "UpsertIngressError",
			Message: fmt.Sprintf("Failed to upsert an Ingress: %s", err.Error()),
		})
		return multierr.Combine(err, statusErr)
	}

	switch result {
	case controllerutil.OperationResultCreated:
		log.Info("Created Ingress")
	case controllerutil.OperationResultUpdated:
		log.Info("Updated Ingress")
	}
	return nil
}

func (r *ApplicationReconciler) ensureNoClusterRoleBindings(ctx context.Context, app *yarotskymev1alpha1.Application, namer Namer) (bool, error) {
	log := log.FromContext(ctx)

	ownedClusterRoleBindings, err := r.getOwnedClusterRoleBindings(ctx, namer)
	if err != nil {
		return false, err
	}

	for _, crb := range ownedClusterRoleBindings {
		log = log.WithValues("kind", crb.Kind, "name", crb.Name)
		log.Info("Deleting ClusterRoleBinding")
		err = r.Delete(ctx, &crb)
		if err != nil {
			log.Error(err, "failed to delete ClusterRoleBinding")
			return false, err
		}
	}

	return true, nil
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

	return ctrl.NewControllerManagedBy(mgr).
		For(&yarotskymev1alpha1.Application{}).
		Owns(&appsv1.Deployment{}).                                                                    // Reconcile when an owned Deployment is changed.
		Owns(&corev1.Service{}).                                                                       // Reconcile when an owned Service is changed.
		Owns(&corev1.ServiceAccount{}).                                                                // Reconcile when an owned AccountService is changed.
		Owns(&networkingv1.Ingress{}).                                                                 // Reconcile when an owned Ingress is changed.
		Owns(&rbacv1.RoleBinding{}).                                                                   // Reconcile when an owned RoleBinding is changed.
		Watches(&rbacv1.ClusterRoleBinding{}, &osdkHandler.EnqueueRequestForAnnotation{Type: r.gk()}). // Reconcile when an "owner" ClusterRoleBinding is changed		WatchesRawSource(
		WatchesRawSource(                                                                              // Reconcile when a new image becomes available for an Application
			&source.Channel{Source: r.ImageUpdateEvents},
			&handler.EnqueueRequestForObject{},
		).
		Complete(r)
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

func (r *ApplicationReconciler) setConditions(ctx context.Context, app *yarotskymev1alpha1.Application, conditions ...metav1.Condition) error {
	if len(conditions) == 0 {
		return nil
	}

	log := log.FromContext(ctx)
	log.Info("Setting conditions", "conditions", conditions)

	for _, c := range conditions {
		meta.SetStatusCondition(&app.Status.Conditions, c)
	}
	if err := r.Status().Update(ctx, app); err != nil {
		log.Error(err, "Failed to update Application status")
		return err
	}
	return nil
}
