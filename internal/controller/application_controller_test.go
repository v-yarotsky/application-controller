package controller_test

import (
	"fmt"
	"slices"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/event"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	"git.home.yarotsky.me/vlad/application-controller/internal/images"
	"git.home.yarotsky.me/vlad/application-controller/internal/k8s"
	prometheusv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	traefikv1alpha1 "github.com/traefik/traefik/v2/pkg/provider/kubernetes/crd/traefikio/v1alpha1"
)

func TestApplicationController(t *testing.T) {
	initializeTestEnvironment(t)
	t.Cleanup(func() {
		tearDownTestEnvironment(t)
	})

	makeApp := func(name string, imageRef *images.ImageRef) yarotskymev1alpha1.Application {
		return yarotskymev1alpha1.Application{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			Spec: yarotskymev1alpha1.ApplicationSpec{
				Image: yarotskymev1alpha1.ImageSpec{
					Repository:      imageRef.Repository,
					VersionStrategy: "Digest",
					Digest: &yarotskymev1alpha1.VersionStrategyDigestSpec{
						Tag: "latest",
					},
				},
				Ports: []corev1.ContainerPort{
					{
						ContainerPort: 8080,
						Name:          "http",
						Protocol:      corev1.ProtocolTCP,
					},
					{
						ContainerPort: 8192,
						Name:          "metrics",
						Protocol:      corev1.ProtocolTCP,
					},
					{
						ContainerPort: 22000,
						Name:          "listen-udp",
						Protocol:      corev1.ProtocolUDP,
					},
				},
				Ingress: &yarotskymev1alpha1.Ingress{
					Host: "dashboard.home.yarotsky.me",
				},
				LoadBalancer: &yarotskymev1alpha1.LoadBalancer{
					Host:      "endpoint.home.yarotsky.me",
					PortNames: []string{"listen-udp"},
				},
				Metrics: &yarotskymev1alpha1.Metrics{
					Enabled: true,
				},
				Roles: []yarotskymev1alpha1.ScopedRoleRef{
					{
						RoleRef: rbacv1.RoleRef{
							APIGroup: "rbac.authorization.k8s.io",
							Kind:     "ClusterRole",
							Name:     "my-cluster-role",
						},
						Scope: yarotskymev1alpha1.RoleBindingScopePointer(yarotskymev1alpha1.RoleBindingScopeCluster),
					},
					{
						RoleRef: rbacv1.RoleRef{
							APIGroup: "rbac.authorization.k8s.io",
							Kind:     "ClusterRole",
							Name:     "my-cluster-role-for-namespace",
						},
					},
					{
						RoleRef: rbacv1.RoleRef{
							APIGroup: "rbac.authorization.k8s.io",
							Kind:     "Role",
							Name:     "my-role",
						},
					},
				},
				Volumes: []yarotskymev1alpha1.Volume{
					{
						Volume: corev1.Volume{
							Name: "data",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: "data",
								},
							},
						},
						MountPath: "/data",
					},
				},

				CronJobs: []yarotskymev1alpha1.CronJobSpec{
					{
						Name:     "dailyjob",
						Schedule: "@daily",
						Command:  []string{"/bin/the-daily-thing"},
					},
				},
			},
		}
	}

	t.Run("Should create Application custom resources", func(t *testing.T) {
		imageRef := registry.MustUpsertTag("app1", "latest")
		app := makeApp("app1", imageRef)
		require.NoError(t, k8sClient.Create(ctx, &app))

		// Create a service account
		serviceAccountName := mkName(app.Namespace, app.Name)
		var serviceAccount corev1.ServiceAccount
		EventuallyGetObject(t, serviceAccountName, &serviceAccount)

		// Create cluster role bindings for ClusterRoles
		var crb rbacv1.ClusterRoleBinding
		EventuallyGetObject(t, mkName("", "default-app1-clusterrole-my-cluster-role"), &crb, func(t require.TestingT) {
			assert.Contains(t, crb.Subjects, rbacv1.Subject{
				APIGroup:  "",
				Kind:      "ServiceAccount",
				Name:      serviceAccountName.Name,
				Namespace: serviceAccountName.Namespace,
			})

			assert.Equal(t, rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "ClusterRole",
				Name:     "my-cluster-role",
			}, crb.RoleRef)
		})

		// Create role bindings for ClusterRoles
		var rb rbacv1.RoleBinding
		EventuallyGetObject(t, mkName(app.Namespace, "app1-clusterrole-my-cluster-role-for-namespace"), &rb, func(t require.TestingT) {
			assert.Contains(t, rb.Subjects, rbacv1.Subject{
				APIGroup:  "",
				Kind:      "ServiceAccount",
				Name:      serviceAccountName.Name,
				Namespace: serviceAccountName.Namespace,
			})

			assert.Equal(t, rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "ClusterRole",
				Name:     "my-cluster-role-for-namespace",
			}, rb.RoleRef)
		})

		// Create role bindings for Roles
		var rb2 rbacv1.RoleBinding
		EventuallyGetObject(t, mkName(app.Namespace, "app1-role-my-role"), &rb2, func(t require.TestingT) {
			assert.Contains(t, rb2.Subjects, rbacv1.Subject{
				APIGroup:  "",
				Kind:      "ServiceAccount",
				Name:      serviceAccountName.Name,
				Namespace: serviceAccountName.Namespace,
			})

			assert.Equal(t, rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "Role",
				Name:     "my-role",
			}, rb2.RoleRef)
		})

		// Create a deployment
		var deploy appsv1.Deployment
		EventuallyGetObject(t, mkName(app.Namespace, app.Name), &deploy, func(t require.TestingT) {
			assert.Equal(t, serviceAccountName.Name, deploy.Spec.Template.Spec.ServiceAccountName)

			assert.True(t, slices.ContainsFunc(deploy.Spec.Template.Spec.Volumes, func(vol corev1.Volume) bool {
				return vol.Name == "data" && vol.VolumeSource.PersistentVolumeClaim.ClaimName == "data"
			}))

			assert.Len(t, deploy.Spec.Template.Spec.Containers, 1)
			if len(deploy.Spec.Template.Spec.Containers) != 1 {
				return
			}
			c := deploy.Spec.Template.Spec.Containers[0]
			assert.Equal(t, imageRef.String(), c.Image)
			assert.Len(t, c.Ports, 3)
			assert.True(t, slices.ContainsFunc(c.Ports, func(p corev1.ContainerPort) bool {
				return p.Name == "http" &&
					p.ContainerPort == int32(8080) &&
					p.Protocol == corev1.ProtocolTCP
			}))
			assert.True(t, slices.ContainsFunc(c.Ports, func(p corev1.ContainerPort) bool {
				return p.Name == "metrics" &&
					p.ContainerPort == int32(8192) &&
					p.Protocol == corev1.ProtocolTCP
			}))
			assert.True(t, slices.ContainsFunc(c.Ports, func(p corev1.ContainerPort) bool {
				return p.Name == "listen-udp" &&
					p.ContainerPort == int32(22000) &&
					p.Protocol == corev1.ProtocolUDP
			}))
			assert.Contains(t, c.VolumeMounts, corev1.VolumeMount{
				Name:      "data",
				MountPath: "/data",
			})
		})

		// Create a service
		serviceName := mkName(app.Namespace, app.Name)
		var service corev1.Service
		EventuallyGetObject(t, serviceName, &service, func(t require.TestingT) {
			assert.Contains(t, service.Spec.Ports, corev1.ServicePort{
				Name:       "http",
				TargetPort: intstr.FromString("http"),
				Protocol:   corev1.ProtocolTCP,
				Port:       8080,
			})

			assert.Equal(t, map[string]string{
				"app.kubernetes.io/name":       app.Name,
				"app.kubernetes.io/managed-by": "application-controller",
				"app.kubernetes.io/instance":   "default",
			}, service.Spec.Selector)
		})

		// Create an IngressRoute
		var ingressRoute traefikv1alpha1.IngressRoute
		EventuallyGetObject(t, mkName(app.Namespace, app.Name), &ingressRoute, func(t require.TestingT) {
			assert.Contains(t, ingressRoute.Spec.Routes, traefikv1alpha1.Route{
				Kind:  "Rule",
				Match: "Host(`dashboard.home.yarotsky.me`)",
				Services: []traefikv1alpha1.Service{
					{
						LoadBalancerSpec: traefikv1alpha1.LoadBalancerSpec{
							Kind:      "Service",
							Namespace: app.Namespace,
							Name:      app.Name,
							Port:      intstr.FromString("http"),
						},
					},
				},
				Middlewares: []traefikv1alpha1.MiddlewareRef{
					{
						Namespace: "kube-system",
						Name:      "foo",
					},
				},
			})
		})

		// Create a LoadBalancer service
		lbServiceName := mkName(app.Namespace, fmt.Sprintf("%s-loadbalancer", app.Name))
		var lbService corev1.Service
		EventuallyGetObject(t, lbServiceName, &lbService, func(t require.TestingT) {
			assert.Contains(t, lbService.Annotations, "external-dns.alpha.kubernetes.io/hostname")
			assert.Equal(t, "endpoint.home.yarotsky.me", lbService.Annotations["external-dns.alpha.kubernetes.io/hostname"])

			assert.True(t, slices.ContainsFunc(lbService.Spec.Ports, func(p corev1.ServicePort) bool {
				return p.Name == "listen-udp" &&
					p.TargetPort == intstr.FromString("listen-udp") &&
					p.Protocol == corev1.ProtocolUDP &&
					p.Port == int32(22000)
			}))

			assert.Equal(t, map[string]string{
				"app.kubernetes.io/name":       app.Name,
				"app.kubernetes.io/managed-by": "application-controller",
				"app.kubernetes.io/instance":   "default",
			}, lbService.Spec.Selector)
		})

		// Create the PodMonitor
		var pm prometheusv1.PodMonitor
		pmName := mkName(app.Namespace, app.Name)
		EventuallyGetObject(t, pmName, &pm, func(t require.TestingT) {
			assert.ElementsMatch(t, pm.Spec.PodMetricsEndpoints, []prometheusv1.PodMetricsEndpoint{{
				Port: "metrics",
				Path: "/metrics",
			}})

			assert.Equal(t, metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app.kubernetes.io/name":       app.Name,
					"app.kubernetes.io/managed-by": "application-controller",
					"app.kubernetes.io/instance":   "default",
				},
			}, pm.Spec.Selector)
		})

		// Create CronJobs
		var cronJob batchv1.CronJob
		EventuallyGetObject(t, mkName(app.Namespace, "app1-dailyjob"), &cronJob, func(t require.TestingT) {
			assert.Equal(t, serviceAccountName.Name, cronJob.Spec.JobTemplate.Spec.Template.Spec.ServiceAccountName)

			assert.True(t, slices.ContainsFunc(cronJob.Spec.JobTemplate.Spec.Template.Spec.Volumes, func(vol corev1.Volume) bool {
				return vol.Name == "data" && vol.VolumeSource.PersistentVolumeClaim.ClaimName == "data"
			}))

			assert.Len(t, cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers, 1)
			if len(cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers) != 1 {
				return
			}
			c := cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers[0]
			assert.Equal(t, imageRef.String(), c.Image)
			assert.Len(t, c.Ports, 0)
			assert.Contains(t, c.VolumeMounts, corev1.VolumeMount{
				Name:      "data",
				MountPath: "/data",
			})
		})
	})

	t.Run("Should update managed deployments and cronjobs when a new image is available", func(t *testing.T) {
		imageRef := registry.MustUpsertTag("app2", "latest")
		app := makeApp("app2", imageRef)
		appName := mkName(app.Namespace, app.Name)
		require.NoError(t, k8sClient.Create(ctx, &app))

		deployName := mkName(app.Namespace, app.Name)
		var deploy appsv1.Deployment

		cronJobName := mkName(app.Namespace, "app2-dailyjob")
		var cronJob batchv1.CronJob

		// Create a deployment with the current image
		EventuallyGetObject(t, deployName, &deploy, func(t require.TestingT) {
			assert.Len(t, deploy.Spec.Template.Spec.Containers, 1)
			assert.True(t, slices.ContainsFunc(deploy.Spec.Template.Spec.Containers, func(c corev1.Container) bool {
				return c.Image == imageRef.String()
			}))
		})

		// Create a CronJob with the current image
		EventuallyGetObject(t, cronJobName, &cronJob, func(t require.TestingT) {
			assert.Len(t, cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers, 1)
			assert.True(t, slices.ContainsFunc(cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers, func(c corev1.Container) bool {
				return c.Image == imageRef.String()
			}))
		})

		// Eventually update the image
		newImageDigestRef := registry.MustUpsertTag("app2", "latest")
		imageUpdateEvents <- event.GenericEvent{Object: &app}

		EventuallyGetObject(t, deployName, &deploy, func(t require.TestingT) {
			assert.Len(t, deploy.Spec.Template.Spec.Containers, 1)
			assert.True(t, slices.ContainsFunc(deploy.Spec.Template.Spec.Containers, func(c corev1.Container) bool {
				return c.Image == newImageDigestRef.String()
			}))
		})

		EventuallyGetObject(t, cronJobName, &cronJob, func(t require.TestingT) {
			assert.Len(t, cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers, 1)
			assert.True(t, slices.ContainsFunc(cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers, func(c corev1.Container) bool {
				return c.Image == newImageDigestRef.String()
			}))
		})

		EventuallyGetObject(t, appName, &app, func(t require.TestingT) {
			assert.Equal(t, newImageDigestRef.String(), app.Status.Image)
		})
	})

	t.Run("Should properly delete created ClusterRoleBinding objects", func(t *testing.T) {
		imageRef := registry.MustUpsertTag("app4", "latest")
		app := makeApp("app4", imageRef)

		crbName := mkName("", "default-app4-clusterrole-my-cluster-role")
		var crb rbacv1.ClusterRoleBinding

		require.NoError(t, k8sClient.Create(ctx, &app))

		EventuallyGetObject(t, crbName, &crb)

		// Delete the CluseterRoleBinding objects
		require.NoError(t, k8sClient.Delete(ctx, &app))

		EventuallyNotFindObject(t, crbName, &crb)
	})

	t.Run("Should update the deployment when container configuration changes", func(t *testing.T) {
		imageRef := registry.MustUpsertTag("app5", "latest")
		app := makeApp("app5", imageRef)
		require.NoError(t, k8sClient.Create(ctx, &app))

		deployName := mkName(app.Namespace, app.Name)
		var deploy appsv1.Deployment

		// Create a deployment
		EventuallyGetObject(t, deployName, &deploy)

		// Update the Application
		EventuallyUpdateApp(t, &app, func() {
			app.Spec.Env = []corev1.EnvVar{
				{
					Name:  "MY_ENV_VAR",
					Value: "FOO",
				},
			}
		})

		// Eventually update the deployment
		EventuallyGetObject(t, deployName, &deploy, func(t require.TestingT) {
			assert.Len(t, deploy.Spec.Template.Spec.Containers, 1)
			assert.True(t, slices.ContainsFunc(deploy.Spec.Template.Spec.Containers, func(c corev1.Container) bool {
				return slices.Contains(c.Env, corev1.EnvVar{Name: "MY_ENV_VAR", Value: "FOO"})
			}))
		})
	})

	t.Run("Should update cronjobs when container configuration changes", func(t *testing.T) {
		imageRef := registry.MustUpsertTag("app14", "latest")
		app := makeApp("app14", imageRef)
		require.NoError(t, k8sClient.Create(ctx, &app))

		cronJobName := mkName(app.Namespace, "app14-dailyjob")
		var cronJob batchv1.CronJob

		// Create a CronJob
		EventuallyGetObject(t, cronJobName, &cronJob)

		// Update the Application
		EventuallyUpdateApp(t, &app, func() {
			app.Spec.Env = []corev1.EnvVar{
				{
					Name:  "MY_ENV_VAR",
					Value: "FOO",
				},
			}
		})

		// Eventually update the CronJob
		EventuallyGetObject(t, cronJobName, &cronJob, func(t require.TestingT) {
			assert.Len(t, cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers, 1)
			assert.True(t, slices.ContainsFunc(cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers, func(c corev1.Container) bool {
				return slices.Contains(c.Env, corev1.EnvVar{Name: "MY_ENV_VAR", Value: "FOO"})
			}))
		})
	})

	t.Run("Should remove rolebindings for removed roles", func(t *testing.T) {
		imageRef := registry.MustUpsertTag("app6", "latest")
		app := makeApp("app6", imageRef)
		require.NoError(t, k8sClient.Create(ctx, &app))

		var rb, rbClusterRole rbacv1.RoleBinding
		var crb rbacv1.ClusterRoleBinding
		rbName := mkName(app.Namespace, "app6-role-my-role")
		rbClusterRoleName := mkName(app.Namespace, "app6-clusterrole-my-cluster-role-for-namespace")
		crbName := mkName("", "default-app6-clusterrole-my-cluster-role")
		appName := mkName(app.Namespace, app.Name)

		// Create the role bindings
		EventuallyGetObject(t, rbName, &rb)
		EventuallyGetObject(t, rbClusterRoleName, &rbClusterRole)
		EventuallyGetObject(t, crbName, &crb)
		EventuallyGetObject(t, appName, &app)

		// Update the Application
		EventuallyUpdateApp(t, &app, func() {
			app.Spec.Roles = []yarotskymev1alpha1.ScopedRoleRef{}
		})

		// Eventually remove the role bindings
		EventuallyNotFindObject(t, rbName, &rb)
		EventuallyNotFindObject(t, rbClusterRoleName, &rbClusterRole)
		EventuallyNotFindObject(t, crbName, &crb)
	})

	t.Run("Should backfill missing RoleBindings and ClusterRoleBindings", func(t *testing.T) {
		imageRef := registry.MustUpsertTag("app7", "latest")
		app := makeApp("app7", imageRef)
		require.NoError(t, k8sClient.Create(ctx, &app))

		var rb, rbClusterRole rbacv1.RoleBinding
		var crb rbacv1.ClusterRoleBinding
		rbName := mkName(app.Namespace, "app7-role-my-role")
		rbClusterRoleName := mkName(app.Namespace, "app7-clusterrole-my-cluster-role-for-namespace")
		crbName := mkName("", "default-app7-clusterrole-my-cluster-role")

		// Create the role bindings
		EventuallyGetObject(t, rbName, &rb)
		EventuallyGetObject(t, rbClusterRoleName, &rbClusterRole)
		EventuallyGetObject(t, crbName, &crb)

		// Manually delete a RoleBinding
		err := k8sClient.Delete(ctx, &rb)
		require.NoError(t, err)

		// Eventually backfills the RoleBinding
		EventuallyGetObject(t, rbName, &rb)

		// Manually delete a RoleBinding for a ClusterRole
		err = k8sClient.Delete(ctx, &rbClusterRole)
		require.NoError(t, err)

		// Eventually backfills the RoleBinding for a ClusterRole
		EventuallyGetObject(t, rbClusterRoleName, &rbClusterRole)

		// Manually delete a ClusterRoleBinding
		err = k8sClient.Delete(ctx, &crb)
		require.NoError(t, err)

		// Eventually backfills the ClusterRoleBinding
		EventuallyGetObject(t, crbName, &crb)
	})

	t.Run("Should set status conditions", func(t *testing.T) {
		imageRef := registry.MustUpsertTag("app8", "latest")
		app := makeApp("app8", imageRef)
		require.NoError(t, k8sClient.Create(ctx, &app))

		// Sets the conditions after creation
		EventuallyHaveCondition(t, &app, "Ready", metav1.ConditionUnknown, "Reconciling")

		// An issue creating an IngressRoute
		EventuallyUpdateApp(t, &app, func() {
			app.Spec.Ingress.Host = "---"
		})

		// Sets the Ready status condition to False
		EventuallyHaveCondition(t, &app, "Ready", metav1.ConditionFalse, "IngressRouteUpsertFailed")

		// Fix the ingress
		EventuallyUpdateApp(t, &app, func() {
			app.Spec.Ingress.Host = "foo.example.com"
		})

		// Deployment becomes Available
		deployName := mkName(app.Namespace, app.Name)
		SetDeploymentAvailableStatus(t, deployName, true, "MinimumReplicasAvailable")

		// Sets the Ready status condition to True
		EventuallyHaveCondition(t, &app, "Ready", metav1.ConditionTrue, "MinimumReplicasAvailable")

		// Deployment becomes unavailable
		SetDeploymentAvailableStatus(t, deployName, false, "MinimumReplicasUnavailable")

		// Sets the Ready status condition to False
		EventuallyHaveCondition(t, &app, "Ready", metav1.ConditionFalse, "MinimumReplicasUnavailable")
	})

	t.Run("Should create PodMonitor with overrides", func(t *testing.T) {
		imageRef := registry.MustUpsertTag("app9", "latest")
		app := makeApp("app9", imageRef)
		app.Spec.Metrics = &yarotskymev1alpha1.Metrics{
			Enabled:  true,
			PortName: "http",
			Path:     "/mymetrics",
		}
		require.NoError(t, k8sClient.Create(ctx, &app))

		// Creates the PodMonitor
		var pm prometheusv1.PodMonitor
		pmName := mkName(app.Namespace, app.Name)
		EventuallyGetObject(t, pmName, &pm, func(t require.TestingT) {
			assert.ElementsMatch(t, pm.Spec.PodMetricsEndpoints, []prometheusv1.PodMetricsEndpoint{{
				Port: "http",
				Path: "/mymetrics",
			}})
		})
	})

	t.Run("Should remove PodMonitor when disabled", func(t *testing.T) {
		imageRef := registry.MustUpsertTag("app10", "latest")
		app := makeApp("app10", imageRef)
		require.NoError(t, k8sClient.Create(ctx, &app))

		name := mkName(app.Namespace, app.Name)
		var mon prometheusv1.PodMonitor

		// Create the PodMonitor
		EventuallyGetObject(t, name, &mon)

		// Update the Application
		EventuallyUpdateApp(t, &app, func() {
			app.Spec.Metrics.Enabled = false
		})

		// Eventually removes the PodMonitor
		EventuallyNotFindObject(t, name, &mon)
	})

	t.Run("Should determine whether Prometheus is supported", func(t *testing.T) {
		hasPrometheus, err := k8s.IsPrometheusOperatorInstalled(cfg)
		require.NoError(t, err)
		assert.True(t, hasPrometheus)
	})

	t.Run("Should remove IngressRoute when disabled", func(t *testing.T) {
		imageRef := registry.MustUpsertTag("app11", "latest")
		app := makeApp("app11", imageRef)
		require.NoError(t, k8sClient.Create(ctx, &app))

		name := mkName(app.Namespace, app.Name)
		var ing traefikv1alpha1.IngressRoute

		// Create the IngressRoute
		EventuallyGetObject(t, name, &ing)

		// Update the Application
		EventuallyUpdateApp(t, &app, func() {
			app.Spec.Ingress = nil
		})

		// Eventually removes the ingress
		EventuallyNotFindObject(t, name, &ing)
	})

	t.Run("Should remove Load Balancer service when disabled", func(t *testing.T) {
		imageRef := registry.MustUpsertTag("app12", "latest")
		app := makeApp("app12", imageRef)
		require.NoError(t, k8sClient.Create(ctx, &app))

		name := mkName(app.Namespace, fmt.Sprintf("%s-loadbalancer", app.Name))
		var svc corev1.Service

		// Create the LoadBalancer Service
		EventuallyGetObject(t, name, &svc)

		// Update the Application
		EventuallyUpdateApp(t, &app, func() {
			app.Spec.LoadBalancer = nil
		})

		// Eventually removes the LoadBalancer Service
		EventuallyNotFindObject(t, name, &svc)
	})

	t.Run("Should remove CronJobs", func(t *testing.T) {
		imageRef := registry.MustUpsertTag("app13", "latest")
		app := makeApp("app13", imageRef)
		require.NoError(t, k8sClient.Create(ctx, &app))

		var cj batchv1.CronJob
		cjName := mkName(app.Namespace, "app13-dailyjob")

		// Create the CronJobs
		EventuallyGetObject(t, cjName, &cj)

		// Update the Application
		EventuallyUpdateApp(t, &app, func() {
			app.Spec.CronJobs = []yarotskymev1alpha1.CronJobSpec{}
		})

		// Eventually remove the CronJobs
		EventuallyNotFindObject(t, cjName, &cj)
	})

}
