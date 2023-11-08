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

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/source"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	"git.home.yarotsky.me/vlad/application-controller/internal/images"
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
const ClusterRoleBindingOwnerAnnotationName = "application.yarotsky.me/owned-by"

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
			err := r.ensureNoClusterRoleBindings(ctx, &app)
			if err != nil {
				return ctrl.Result{}, err
			}

			controllerutil.RemoveFinalizer(&app, FinalizerName)
			if err := r.Update(ctx, &app); err != nil {
				return ctrl.Result{}, err
			}
		}
		// we are in the process of deletion
		return ctrl.Result{}, nil
	}

	if err := r.ensureServiceAccount(ctx, &app); err != nil {
		log.Error(err, "failed to ensure a ServiceAccount exists for the Application")
		return ctrl.Result{}, err
	}

	if err := r.ensureRoleBindings(ctx, &app); err != nil {
		log.Error(err, "failed to ensure a RoleBindings exists for the Application")
		return ctrl.Result{}, err
	}

	if err := r.ensureDeployment(ctx, &app); err != nil {
		log.Error(err, "failed to ensure a Deployment exists for the Application")
		return ctrl.Result{}, err
	}

	if err := r.ensureService(ctx, &app); err != nil {
		log.Error(err, "failed to ensure a Service exists for the Application")
		return ctrl.Result{}, err
	}

	if err := r.ensureIngress(ctx, &app); err != nil {
		log.Error(err, "failed to ensure a Ingress exists for the Application")
		return ctrl.Result{}, err
	}

	log.Info("Application is up to date.")
	return ctrl.Result{}, nil
}

func (r *ApplicationReconciler) ensureDeployment(ctx context.Context, app *yarotskymev1alpha1.Application) error {
	name := app.DeploymentName()
	log := log.FromContext(ctx).WithValues("deploymentname", name.String())

	deploy := appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name.Name,
			Namespace: name.Namespace,
		},
	}

	imgRef, err := r.ImageFinder.FindImage(ctx, app.Spec.Image)
	if err != nil {
		log.Error(err, "failed to set controller reference on Deployment")
		return err
	}

	result, err := controllerutil.CreateOrPatch(ctx, r.Client, &deploy, func() error {
		selectorLabels := app.SelectorLabels()

		deploy.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: selectorLabels,
		}

		deploy.Spec.Template.ObjectMeta = metav1.ObjectMeta{
			Labels: selectorLabels,
		}

		podTemplateSpec := &deploy.Spec.Template.Spec
		podTemplateSpec.ServiceAccountName = app.ServiceAccountName().Name

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

		if err := controllerutil.SetControllerReference(app, &deploy, r.Scheme); err != nil {
			log.Error(err, "failed to set controller reference on Deployment")
			return err
		}
		return nil
	})
	if err != nil {
		log.Error(err, "failed to create/update the Deployment")
		return err
	}

	switch result {
	case controllerutil.OperationResultCreated:
		log.Info("Created Deployment")
	case controllerutil.OperationResultUpdated:
		log.Info("Updated Deployment")
	}
	return nil
}

func (r *ApplicationReconciler) ensureServiceAccount(ctx context.Context, app *yarotskymev1alpha1.Application) error {
	name := app.ServiceAccountName()
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
		return err
	}

	switch result {
	case controllerutil.OperationResultCreated:
		log.Info("Created ServiceAccount")
	case controllerutil.OperationResultUpdated:
		log.Info("Updated ServiceAccount")
	}
	return nil
}

// This is necessary to be able to query a list of RoleBinding objects owned by an Application
const roleBindingOwnerKey = "metadata.controller"

func (r *ApplicationReconciler) ensureRoleBindings(ctx context.Context, app *yarotskymev1alpha1.Application) error {
	log := log.FromContext(ctx)

	serviceAccountName := app.ServiceAccountName()

	// TODO: This did not work with custom indexer! Make note!
	// ownedRoleBindings := &metav1.PartialObjectMetadataList{}
	// ownedRoleBindings.SetGroupVersionKind(rbacv1.SchemeGroupVersion.WithKind("RoleBinding"))

	ownedRoleBindings := &rbacv1.RoleBindingList{}

	lookInAllNamespaces := client.InNamespace("")
	err := r.Client.List(ctx, ownedRoleBindings, lookInAllNamespaces, client.MatchingFields(map[string]string{roleBindingOwnerKey: string(app.UID)}))
	if err != nil {
		log.Error(err, "failed to get the list of owned RoleBinding objects")
		return err
	}

	seenRoleBindingsSet := make(map[string]bool, len(ownedRoleBindings.Items))
	for _, rb := range ownedRoleBindings.Items {
		seenRoleBindingsSet[rb.Name] = false
	}

	ownedClusterRoleBindings := &rbacv1.ClusterRoleBindingList{}
	err = r.Client.List(ctx, ownedClusterRoleBindings, client.MatchingFields(map[string]string{roleBindingOwnerKey: string(app.UID)}))
	if err != nil {
		log.Error(err, "failed to get the list of owned ClusterRoleBinding objects")
		return err
	}

	seenClusterRoleBindingsSet := make(map[string]bool, len(ownedClusterRoleBindings.Items))
	for _, crb := range ownedClusterRoleBindings.Items {
		seenClusterRoleBindingsSet[crb.Name] = false
	}

	for _, roleRef := range app.Spec.Roles {
		log = log.WithValues("kind", roleRef.Kind, "name", roleRef.Name)

		name, err := app.RoleBindingNameForRoleRef(roleRef.RoleRef)
		if err != nil {
			return err
		}

		scope := yarotskymev1alpha1.RoleBindingScopeNamespace
		if roleRef.Scope != nil {
			scope = *roleRef.Scope
		}

		if scope == yarotskymev1alpha1.RoleBindingScopeNamespace {
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
				return err
			}

			switch result {
			case controllerutil.OperationResultCreated:
				log.Info("RoleBinding created")
			case controllerutil.OperationResultUpdated:
				log.Info("RoleBinding updated")
			}
			seenRoleBindingsSet[name.Name] = true

		} else if scope == yarotskymev1alpha1.RoleBindingScopeCluster {
			if !(roleRef.APIGroup == "rbac.authorization.k8s.io" && roleRef.Kind == "ClusterRole") {
				err := fmt.Errorf("ClusterRoleBindings can only be created for ClusterRoles")
				log.Error(err, "Failed to create ClusterRoleBinding")
				return err
			}
			rb := rbacv1.ClusterRoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name: name.Name,
				},
			}
			result, err := controllerutil.CreateOrPatch(ctx, r.Client, &rb, func() error {
				rb.SetAnnotations(map[string]string{ClusterRoleBindingOwnerAnnotationName: string(app.UID)})

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
				return err
			}

			switch result {
			case controllerutil.OperationResultCreated:
				log.Info("ClusterRoleBinding created")
			case controllerutil.OperationResultUpdated:
				log.Info("ClusterRoleBinding updated")
			}
			seenClusterRoleBindingsSet[name.Name] = true
		}
	}

	for rbName, seen := range seenRoleBindingsSet {
		if seen {
			continue
		}
		log = log.WithValues("name", rbName)
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

	for crbName, seen := range seenClusterRoleBindingsSet {
		if seen {
			continue
		}
		log = log.WithValues("name", crbName)
		log.Info("ClusterRoleBinding is not needed according to spec. Deleting.")
		crb := rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name: crbName,
			},
		}
		err := r.Client.Delete(ctx, &crb)
		if err != nil {
			log.Error(err, "failed to delete ClusterRoleBinding")
			return err
		}
	}

	return nil
}

func (r *ApplicationReconciler) ensureService(ctx context.Context, app *yarotskymev1alpha1.Application) error {
	name := app.ServiceName()
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
		svc.Spec.Selector = app.SelectorLabels()
		if err := controllerutil.SetControllerReference(app, &svc, r.Scheme); err != nil {
			log.Error(err, "failed to set controller reference on Service")
			return err
		}
		return nil
	})

	if err != nil {
		log.Error(err, "failed to create/update the Service")
		return err
	}

	switch result {
	case controllerutil.OperationResultCreated:
		log.Info("Service Deployment")
	case controllerutil.OperationResultUpdated:
		log.Info("Service Deployment")
	}
	return nil
}

func (r *ApplicationReconciler) ensureIngress(ctx context.Context, app *yarotskymev1alpha1.Application) error {
	name := app.IngressName()
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
										Name: app.ServiceName().Name,
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
		return err
	}

	switch result {
	case controllerutil.OperationResultCreated:
		log.Info("Created Ingress")
	case controllerutil.OperationResultUpdated:
		log.Info("Updated Ingress")
	}
	return nil
}

func (r *ApplicationReconciler) ensureNoClusterRoleBindings(ctx context.Context, app *yarotskymev1alpha1.Application) error {
	log := log.FromContext(ctx)

	ownedClusterRoleBindings := &rbacv1.ClusterRoleBindingList{}
	err := r.Client.List(ctx, ownedClusterRoleBindings, client.MatchingFields(map[string]string{roleBindingOwnerKey: string(app.UID)}))
	if err != nil {
		log.Error(err, "failed to get the list of owned ClusterRoleBinding objects")
		return err
	}

	for _, crb := range ownedClusterRoleBindings.Items {
		log = log.WithValues("kind", crb.Kind, "name", crb.Name)
		log.Info("Deleting ClusterRoleBinding")
		err = r.Delete(ctx, &crb)
		if err != nil {
			log.Error(err, "failed to delete ClusterRoleBinding")
			return err
		}
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ApplicationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	indexer := mgr.GetFieldIndexer()

	// Index owned RoleBinding objects by owner in our cache
	err := indexer.IndexField(context.Background(), &rbacv1.RoleBinding{}, roleBindingOwnerKey, func(rawObj client.Object) []string {
		rb := rawObj.(*rbacv1.RoleBinding)
		owner := metav1.GetControllerOf(rb)
		if owner == nil {
			return nil
		}
		if owner.APIVersion != "yarotsky.me/v1alpha1" || owner.Kind != "Application" {
			return nil
		}

		return []string{string(owner.UID)}
	})
	if err != nil {
		return err
	}

	// Index "owned" ClusterRoleBinding objects by owner in our cache
	err = indexer.IndexField(context.Background(), &rbacv1.ClusterRoleBinding{}, roleBindingOwnerKey, func(rawObj client.Object) []string {
		rb := rawObj.(*rbacv1.ClusterRoleBinding)
		if ownerUID, ok := rb.GetAnnotations()[ClusterRoleBindingOwnerAnnotationName]; ok {
			return []string{ownerUID}
		}
		return nil
	})
	if err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&yarotskymev1alpha1.Application{}).
		Owns(&appsv1.Deployment{}).     // Trigger reconciliation whenever an owned Deployment is changed.
		Owns(&corev1.Service{}).        // Trigger reconciliation whenever an owned Service is changed.
		Owns(&corev1.ServiceAccount{}). // Trigger reconciliation whenever an owned AccountService is changed.
		Owns(&networkingv1.Ingress{}).  // Trigger reconciliation whenever an owned Ingress is changed.
		Owns(&rbacv1.RoleBinding{}).    // Trigger reconciliation whenever an owned RoleBinding is changed.
		WatchesRawSource(
			&source.Channel{Source: r.ImageUpdateEvents},
			&handler.EnqueueRequestForObject{},
		).
		Complete(r)
}
