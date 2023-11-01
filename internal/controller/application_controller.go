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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ApplicationReconciler reconciles a Application object
type ApplicationReconciler struct {
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
//+kubebuilder:rbac:groups=networking,resources=ingresses,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Application object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
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

	if err := r.ensureDeployment(ctx, &app); err != nil {
		log.Error(err, "failed to ensure a Deployment exists for the Application")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *ApplicationReconciler) ensureDeployment(ctx context.Context, app *yarotskymev1alpha1.Application) error {
	log := log.FromContext(ctx)

	var wantDeploy appsv1.Deployment
	selectorLabels := map[string]string{
		"app.kubernetes.io/name":       app.Name,
		"app.kubernetes.io/managed-by": "application-controller",
		"app.kubernetes.io/instance":   "default",
		"app.kubernetes.io/version":    "0.1.0",
	}
	wantDeploy = appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      app.Name,
			Namespace: app.Namespace,
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
					Containers: []corev1.Container{
						{
							Name:            app.Name,
							Image:           fmt.Sprintf("%s:v0.1.0", app.Spec.Image.Repository),
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
			r.Create(ctx, &wantDeploy)
		}
		log.Error(err, "Failed to retrieve Deployment for Application")
		return err
	}

	// deployment was found
	// check for diff and do r.Update(ctx, gotDeploy)
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ApplicationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&yarotskymev1alpha1.Application{}).
		Owns(&appsv1.Deployment{}). // Trigger reconciliation whenever an owned Deployment is changed.
		Complete(r)
}
