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
	ImageFinder       images.ImageFinder
	imageUpdateEvents chan event.GenericEvent
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=yarotsky.me.yarotsky.me,resources=applications,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=yarotsky.me.yarotsky.me,resources=applications/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=yarotsky.me.yarotsky.me,resources=applications/finalizers,verbs=update
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
		log.Error(err, "unable to fetch Application")
		return ctrl.Result{}, client.IgnoreNotFound(err)
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

	log.Info("Application should be provisioned")
	return ctrl.Result{}, nil
}

func (r *ApplicationReconciler) ensureDeployment(ctx context.Context, app *yarotskymev1alpha1.Application) error {
	log := log.FromContext(ctx)

	var wantDeploy appsv1.Deployment
	selectorLabels := app.SelectorLabels()

	imgRef, err := r.ImageFinder.FindImage(ctx, app.Spec.Image)
	if err != nil {
		log.Error(err, "failed to set controller reference on Deployment")
		return err
	}

	deployName := app.DeploymentName()
	wantDeploy = appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deployName.Name,
			Namespace: deployName.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: selectorLabels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: selectorLabels,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: app.ServiceAccountName().Name,
					Containers: []corev1.Container{
						{
							Name:            app.Name,
							Image:           imgRef,
							ImagePullPolicy: "Always",
							Command:         app.Spec.Command,
							Args:            app.Spec.Args,
							Env:             app.Spec.Env,
							Ports:           app.Spec.Ports,
							Resources:       app.Spec.Resources,
							LivenessProbe:   app.Spec.LivenessProbe,
							ReadinessProbe:  app.Spec.ReadinessProbe,
							StartupProbe:    app.Spec.StartupProbe,
							SecurityContext: app.Spec.SecurityContext,
						},
					},
				},
			},
		},
	}
	if err := controllerutil.SetControllerReference(app, &wantDeploy, r.Scheme); err != nil {
		log.Error(err, "failed to set controller reference on Deployment")
		return err
	}

	gotDeploy := appsv1.Deployment{}

	if err := r.Get(ctx, client.ObjectKeyFromObject(&wantDeploy), &gotDeploy); err != nil {
		if errors.IsNotFound(err) {
			log.Info("Deployment not found for Application. Creating.")
			return r.Create(ctx, &wantDeploy)
		}
		log.Error(err, "Failed to retrieve Deployment for Application")
		return err
	}

	// deployment was found

	// See if the image needs updating
	if currentImgRef := gotDeploy.Spec.Template.Spec.Containers[0].Image; currentImgRef != imgRef {
		log.WithValues("currentImageRef", currentImgRef, "newImageRef", imgRef).Info("Found updated image. Updating Deployment")
		updatedDeploy := gotDeploy.DeepCopy()
		updatedDeploy.Spec.Template.Spec.Containers[0].Image = imgRef
		if err := r.Patch(ctx, updatedDeploy, client.StrategicMergeFrom(&gotDeploy)); err != nil {
			log.Error(err, "Failed to patch Deployment for Application")
			return err
		}
	}
	return nil
}

func (r *ApplicationReconciler) ensureServiceAccount(ctx context.Context, app *yarotskymev1alpha1.Application) error {
	log := log.FromContext(ctx)

	serviceAccountName := app.ServiceAccountName()

	var wantServiceAccount corev1.ServiceAccount
	wantServiceAccount = corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceAccountName.Name,
			Namespace: serviceAccountName.Namespace,
		},
	}
	if err := controllerutil.SetControllerReference(app, &wantServiceAccount, r.Scheme); err != nil {
		log.Error(err, "failed to set controller reference on ServiceAccount")
		return err
	}

	gotServiceAccount := corev1.ServiceAccount{}

	if err := r.Get(ctx, client.ObjectKeyFromObject(&wantServiceAccount), &gotServiceAccount); err != nil {
		if errors.IsNotFound(err) {
			log.Info("ServiceAccount not found for Application. Creating.")
			return r.Create(ctx, &wantServiceAccount)
		}
		log.Error(err, "Failed to retrieve ServiceAccount for Application")
		return err
	}

	return nil
}

func (r *ApplicationReconciler) ensureRoleBindings(ctx context.Context, app *yarotskymev1alpha1.Application) error {
	log := log.FromContext(ctx)

	serviceAccountName := app.ServiceAccountName()

	// TODO this actually needs to make sure to delete rolebindings, too
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
			want := rbacv1.RoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name.Name,
					Namespace: name.Namespace,
				},
				Subjects: []rbacv1.Subject{
					{
						APIGroup:  "",
						Kind:      "ServiceAccount",
						Name:      serviceAccountName.Name,
						Namespace: serviceAccountName.Namespace,
					},
				},
				RoleRef: roleRef.RoleRef,
			}

			if err := controllerutil.SetControllerReference(app, &want, r.Scheme); err != nil {
				log.Error(err, "failed to set controller reference on RoleBinding")
				return err
			}

			got := rbacv1.RoleBinding{}
			if err := r.Get(ctx, client.ObjectKeyFromObject(&want), &got); err != nil {
				if errors.IsNotFound(err) {
					log.Info("RoleBinding not found for Application. Creating.")
					return r.Create(ctx, &want)
				}
				log.Error(err, "Failed to retrieve RoleBinding for Application")
				return err
			}
		} else if scope == yarotskymev1alpha1.RoleBindingScopeCluster {
			if !(roleRef.APIGroup == "rbac.authorization.k8s.io" && roleRef.Kind == "ClusterRole") {
				err := fmt.Errorf("ClusterRoleBindings can only be created for ClusterRoles")
				log.Error(err, "Failed to create ClusterRoleBinding")
				return err
			}
			want := rbacv1.ClusterRoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name: name.Name,
				},
				Subjects: []rbacv1.Subject{
					{
						APIGroup:  "",
						Kind:      "ServiceAccount",
						Name:      serviceAccountName.Name,
						Namespace: serviceAccountName.Namespace,
					},
				},
				RoleRef: roleRef.RoleRef,
			}

			// TODO ensure we clean up ClusterRoleBindings; can't use owner trackign due
			// to `cluster-scoped resource must not have a namespace-scoped owner, owner's namespace default`

			got := rbacv1.ClusterRoleBinding{}
			if err := r.Get(ctx, client.ObjectKeyFromObject(&want), &got); err != nil {
				if errors.IsNotFound(err) {
					log.Info("ClusterRoleBinding not found for Application. Creating.")
					return r.Create(ctx, &want)
				}
				log.Error(err, "Failed to retrieve ClusterRoleBinding for Application")
				return err
			}
		}
	}

	return nil
}

func (r *ApplicationReconciler) ensureService(ctx context.Context, app *yarotskymev1alpha1.Application) error {
	log := log.FromContext(ctx)

	serviceName := app.ServiceName()

	var wantService corev1.Service
	ports := make([]corev1.ServicePort, 0, len(app.Spec.Ports))
	for _, p := range app.Spec.Ports {
		ports = append(ports, corev1.ServicePort{
			Name:       p.Name,
			TargetPort: intstr.FromString(p.Name),
			Protocol:   p.Protocol,
			Port:       p.ContainerPort,
		})
	}
	wantService = corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName.Name,
			Namespace: serviceName.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: app.SelectorLabels(),
			Ports:    ports,
		},
	}
	if err := controllerutil.SetControllerReference(app, &wantService, r.Scheme); err != nil {
		log.Error(err, "failed to set controller reference on Service")
		return err
	}

	gotService := corev1.Service{}

	if err := r.Get(ctx, client.ObjectKeyFromObject(&wantService), &gotService); err != nil {
		if errors.IsNotFound(err) {
			log.Info("Service not found for Application. Creating.")
			return r.Create(ctx, &wantService)
		}
		log.Error(err, "Failed to retrieve Service for Application")
		return err
	}

	return nil
}

func (r *ApplicationReconciler) ensureIngress(ctx context.Context, app *yarotskymev1alpha1.Application) error {
	log := log.FromContext(ctx)

	if app.Spec.Ingress == nil {
		log.Info("Application does not specify Ingress, skipping Ingress creation.")
		return nil
	}

	ingressName := app.IngressName()
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

	var wantIngress networkingv1.Ingress
	wantIngress = networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ingressName.Name,
			Namespace: ingressName.Namespace,
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: app.Spec.Ingress.IngressClassName,
			Rules: []networkingv1.IngressRule{
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
			},
		},
	}
	if err := controllerutil.SetControllerReference(app, &wantIngress, r.Scheme); err != nil {
		log.Error(err, "failed to set controller reference on Ingress")
		return err
	}

	gotIngress := networkingv1.Ingress{}

	if err := r.Get(ctx, client.ObjectKeyFromObject(&wantIngress), &gotIngress); err != nil {
		if errors.IsNotFound(err) {
			log.Info("Ingress not found for Application. Creating.")
			return r.Create(ctx, &wantIngress)
		}
		log.Error(err, "Failed to retrieve Ingress for Application")
		return err
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ApplicationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&yarotskymev1alpha1.Application{}).
		Owns(&appsv1.Deployment{}).     // Trigger reconciliation whenever an owned Deployment is changed.
		Owns(&corev1.Service{}).        // Trigger reconciliation whenever an owned Service is changed.
		Owns(&corev1.ServiceAccount{}). // Trigger reconciliation whenever an owned AccountService is changed.
		Owns(&networkingv1.Ingress{}).  // Trigger reconciliation whenever an owned Ingress is changed.
		Owns(&rbacv1.RoleBinding{}).    // Trigger reconciliation whenever an owned RoleBinding is changed.
		WatchesRawSource(
			&source.Channel{Source: r.imageUpdateEvents},
			&handler.EnqueueRequestForObject{},
		).
		Complete(r)
}
