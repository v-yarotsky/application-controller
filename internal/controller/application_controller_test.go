package controller

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	"git.home.yarotsky.me/vlad/application-controller/internal/images"
	"git.home.yarotsky.me/vlad/application-controller/internal/testutil"
	prometheusv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

var _ = Describe("Application controller", func() {
	const (
		namespace = "default"
	)

	var (
		registry *testutil.TestRegistry
	)

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
					Digest: yarotskymev1alpha1.VersionStrategyDigestSpec{
						Tag: "latest",
					},
				},
				Ports: []corev1.ContainerPort{
					{
						ContainerPort: 8080,
						Name:          "http",
						Protocol:      corev1.ProtocolTCP,
					},
				},
				Ingress: &yarotskymev1alpha1.Ingress{
					IngressClassName: pointer.String("traefik"),
					Host:             "dashboard.home.yarotsky.me",
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
			},
		}
	}

	BeforeEach(func() {
		registry = testutil.NewTestRegistry(GinkgoT())
	})

	AfterEach(func() {
		registry.Close()
	})

	It("Should create Application custom resources", func(ctx SpecContext) {
		By("Creating an Application CR")
		imageRef := registry.MustUpsertTag("app1", "latest")
		app := makeApp("app1", imageRef)
		Expect(k8sClient.Create(ctx, &app)).Should(Succeed())

		By("Creating a service account")
		serviceAccountName := mkName(app.Namespace, app.Name)
		var serviceAccount corev1.ServiceAccount
		EventuallyGetObject(ctx, serviceAccountName, &serviceAccount)

		By("Creating cluster role bindings for ClusterRoles")
		Eventually(func(g Gomega) {
			var crb rbacv1.ClusterRoleBinding
			err := k8sClient.Get(ctx, mkName("", "default-app1-clusterrole-my-cluster-role"), &crb)
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(crb.Subjects).To(ContainElement(rbacv1.Subject{
				APIGroup:  "",
				Kind:      "ServiceAccount",
				Name:      serviceAccountName.Name,
				Namespace: serviceAccountName.Namespace,
			}))

			g.Expect(crb.RoleRef).To(Equal(rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "ClusterRole",
				Name:     "my-cluster-role",
			}))
		}).WithContext(ctx).Should(Succeed())

		By("Creating role bindings for ClusterRoles")
		Eventually(func(g Gomega) {
			var rb rbacv1.RoleBinding
			err := k8sClient.Get(ctx, mkName(app.Namespace, "app1-clusterrole-my-cluster-role-for-namespace"), &rb)
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(rb.Subjects).To(ContainElement(rbacv1.Subject{
				APIGroup:  "",
				Kind:      "ServiceAccount",
				Name:      serviceAccountName.Name,
				Namespace: serviceAccountName.Namespace,
			}))

			g.Expect(rb.RoleRef).To(Equal(rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "ClusterRole",
				Name:     "my-cluster-role-for-namespace",
			}))
		}).WithContext(ctx).Should(Succeed())

		By("Creating role bindings for Roles")
		Eventually(func(g Gomega) {
			var rb rbacv1.RoleBinding
			err := k8sClient.Get(ctx, mkName(app.Namespace, "app1-role-my-role"), &rb)
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(rb.Subjects).To(ContainElement(rbacv1.Subject{
				APIGroup:  "",
				Kind:      "ServiceAccount",
				Name:      serviceAccountName.Name,
				Namespace: serviceAccountName.Namespace,
			}))

			g.Expect(rb.RoleRef).To(Equal(rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "Role",
				Name:     "my-role",
			}))
		}).WithContext(ctx).Should(Succeed())

		By("Creating a deployment")
		Eventually(func(g Gomega) {
			var deploy appsv1.Deployment
			err := k8sClient.Get(ctx, mkName(app.Namespace, app.Name), &deploy)
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(deploy.Spec.Template.Spec.ServiceAccountName).To(Equal(serviceAccountName.Name))
			g.Expect(deploy.Spec.Template.Spec.Volumes).To(ContainElement(
				SatisfyAll(
					HaveField("Name", "data"),
					HaveField("VolumeSource.PersistentVolumeClaim.ClaimName", "data"),
				),
			))

			g.Expect(deploy.Spec.Template.Spec.Containers).To(HaveLen(1))

			mainContainer := deploy.Spec.Template.Spec.Containers[0]
			g.Expect(mainContainer.Image).To(Equal(imageRef.String()))
			g.Expect(mainContainer.Ports).To(HaveLen(1))
			g.Expect(mainContainer.Ports).To(ContainElement(corev1.ContainerPort{ContainerPort: 8080, Name: "http", Protocol: corev1.ProtocolTCP}))
			g.Expect(mainContainer.VolumeMounts).To(ContainElement(corev1.VolumeMount{
				Name:      "data",
				MountPath: "/data",
			}))
		}).WithContext(ctx).Should(Succeed())

		By("Creating a service")
		serviceName := mkName(app.Namespace, app.Name)
		Eventually(func(g Gomega) {
			var service corev1.Service
			err := k8sClient.Get(ctx, serviceName, &service)
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(service.Spec.Ports).To(ContainElement(corev1.ServicePort{
				Name:       "http",
				TargetPort: intstr.FromString("http"),
				Protocol:   corev1.ProtocolTCP,
				Port:       8080,
			}))

			g.Expect(service.Spec.Selector).To(Equal(map[string]string{
				"app.kubernetes.io/name":       app.Name,
				"app.kubernetes.io/managed-by": "application-controller",
				"app.kubernetes.io/instance":   "default",
				"app.kubernetes.io/version":    "0.1.0",
			}))
		}).WithContext(ctx).Should(Succeed())

		By("Creating an Ingress")
		Eventually(func(g Gomega) {
			var ingress networkingv1.Ingress
			err := k8sClient.Get(ctx, mkName(app.Namespace, app.Name), &ingress)
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(*ingress.Spec.IngressClassName).To(Equal("traefik"))
			g.Expect(ingress.Annotations).To(Equal(map[string]string{
				"foo": "bar",
			}))

			pathType := networkingv1.PathTypeImplementationSpecific
			g.Expect(ingress.Spec.Rules).To(ContainElement(networkingv1.IngressRule{
				Host: "dashboard.home.yarotsky.me",
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{
							{
								Path:     "/",
								PathType: &pathType,
								Backend: networkingv1.IngressBackend{
									Service: &networkingv1.IngressServiceBackend{
										Name: serviceName.Name,
										Port: networkingv1.ServiceBackendPort{
											Name: "http",
										},
									},
								},
							},
						},
					},
				},
			}))
		}).WithContext(ctx).Should(Succeed())
	}, SpecTimeout(5*time.Second))

	It("Should update managed deployments when a new image is available", func(ctx SpecContext) {
		imageRef := registry.MustUpsertTag("app2", "latest")
		app := makeApp("app2", imageRef)
		appName := mkName(app.Namespace, app.Name)
		Expect(k8sClient.Create(ctx, &app)).Should(Succeed())

		deployName := mkName(app.Namespace, app.Name)
		var deploy appsv1.Deployment

		By("Creating a deployment with the current image")
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, deployName, &deploy)
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(deploy.Spec.Template.Spec.Containers).To(HaveLen(1))

			mainContainer := deploy.Spec.Template.Spec.Containers[0]
			g.Expect(mainContainer.Image).To(Equal(imageRef.String()))
		}).WithContext(ctx).Should(Succeed())

		By("Eventually updating the image")
		newImageDigestRef := registry.MustUpsertTag("app2", "latest")
		imageUpdateEvents <- event.GenericEvent{Object: &app}

		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, deployName, &deploy)
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(deploy.Spec.Template.Spec.Containers).To(HaveLen(1))

			mainContainer := deploy.Spec.Template.Spec.Containers[0]
			g.Expect(mainContainer.Image).To(Equal(newImageDigestRef.String()))
		}).WithContext(ctx).Should(Succeed())

		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, appName, &app)
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(app.Status.Image).To(Equal(newImageDigestRef.String()))
		}).WithContext(ctx).Should(Succeed())
	}, SpecTimeout(5*time.Second))

	It("Should use default ingress class when no explicit ingress class is specified", func(ctx SpecContext) {
		imageRef := registry.MustUpsertTag("app3", "latest")
		app := makeApp("app3", imageRef)
		app.Spec.Ingress.IngressClassName = nil

		Expect(k8sClient.Create(ctx, &app)).Should(Succeed())

		By("Creating an Ingress")
		Eventually(func(g Gomega) {
			var ingress networkingv1.Ingress
			err := k8sClient.Get(ctx, mkName(app.Namespace, app.Name), &ingress)
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(*ingress.Spec.IngressClassName).To(Equal("nginx-private"))
		}).WithContext(ctx).Should(Succeed())

	}, SpecTimeout(5*time.Second))

	It("Should properly delete created ClusterRoleBinding objects", func(ctx SpecContext) {
		imageRef := registry.MustUpsertTag("app4", "latest")
		app := makeApp("app4", imageRef)

		crbName := mkName("", "default-app4-clusterrole-my-cluster-role")
		var crb rbacv1.ClusterRoleBinding

		Expect(k8sClient.Create(ctx, &app)).Should(Succeed())

		EventuallyGetObject(ctx, crbName, &crb)

		By("Deleting the CluseterRoleBinding objects")
		Expect(k8sClient.Delete(ctx, &app)).Should(Succeed())

		EventuallyNotFindObject(ctx, crbName, &crb)
	}, SpecTimeout(5*time.Second))

	It("Should update the deployment when container configuration changes", func(ctx SpecContext) {
		imageRef := registry.MustUpsertTag("app5", "latest")
		app := makeApp("app5", imageRef)
		Expect(k8sClient.Create(ctx, &app)).Should(Succeed())

		deployName := mkName(app.Namespace, app.Name)
		var deploy appsv1.Deployment

		By("Creating a deployment")
		EventuallyGetObject(ctx, deployName, &deploy)

		By("Updating the Application")
		EventuallyUpdateApp(ctx, &app, func() {
			app.Spec.Env = []corev1.EnvVar{
				{
					Name:  "MY_ENV_VAR",
					Value: "FOO",
				},
			}
		})

		By("Eventually updating the deployment")
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, deployName, &deploy)
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(deploy.Spec.Template.Spec.Containers).To(HaveLen(1))

			mainContainer := deploy.Spec.Template.Spec.Containers[0]
			g.Expect(mainContainer.Env).To(ContainElement(corev1.EnvVar{Name: "MY_ENV_VAR", Value: "FOO"}))
		}).WithContext(ctx).Should(Succeed())
	}, SpecTimeout(5*time.Second))

	It("Should remove rolebindings for removed roles", func(ctx SpecContext) {
		imageRef := registry.MustUpsertTag("app6", "latest")
		app := makeApp("app6", imageRef)
		Expect(k8sClient.Create(ctx, &app)).Should(Succeed())

		var rb, rbClusterRole rbacv1.RoleBinding
		var crb rbacv1.ClusterRoleBinding
		rbName := mkName(app.Namespace, "app6-role-my-role")
		rbClusterRoleName := mkName(app.Namespace, "app6-clusterrole-my-cluster-role-for-namespace")
		crbName := mkName("", "default-app6-clusterrole-my-cluster-role")
		appName := mkName(app.Namespace, app.Name)

		By("Creating the role bindings")
		EventuallyGetObject(ctx, rbName, &rb)
		EventuallyGetObject(ctx, rbClusterRoleName, &rbClusterRole)
		EventuallyGetObject(ctx, crbName, &crb)
		EventuallyGetObject(ctx, appName, &app)

		By("Updating the Application")
		EventuallyUpdateApp(ctx, &app, func() {
			app.Spec.Roles = []yarotskymev1alpha1.ScopedRoleRef{}
		})

		By("Eventually removing the role bindings")
		EventuallyNotFindObject(ctx, rbName, &rb)
		EventuallyNotFindObject(ctx, rbClusterRoleName, &rbClusterRole)
		EventuallyNotFindObject(ctx, crbName, &crb)
	}, SpecTimeout(5*time.Second))

	It("Should backfill missing RoleBindings and ClusterRoleBindings", func(ctx SpecContext) {
		imageRef := registry.MustUpsertTag("app7", "latest")
		app := makeApp("app7", imageRef)
		Expect(k8sClient.Create(ctx, &app)).Should(Succeed())

		var rb, rbClusterRole rbacv1.RoleBinding
		var crb rbacv1.ClusterRoleBinding
		rbName := mkName(app.Namespace, "app7-role-my-role")
		rbClusterRoleName := mkName(app.Namespace, "app7-clusterrole-my-cluster-role-for-namespace")
		crbName := mkName("", "default-app7-clusterrole-my-cluster-role")

		By("Creating the role bindings")
		EventuallyGetObject(ctx, rbName, &rb)
		EventuallyGetObject(ctx, rbClusterRoleName, &rbClusterRole)
		EventuallyGetObject(ctx, crbName, &crb)

		By("Manually deleting a RoleBinding")
		err := k8sClient.Delete(ctx, &rb)
		Expect(err).NotTo(HaveOccurred())

		By("Eventually backfilling the RoleBinding")
		EventuallyGetObject(ctx, rbName, &rb)

		By("Manually deleting a RoleBinding for a ClusterRole")
		err = k8sClient.Delete(ctx, &rbClusterRole)
		Expect(err).NotTo(HaveOccurred())

		By("Eventually backfilling the RoleBinding for a ClusterRole")
		EventuallyGetObject(ctx, rbClusterRoleName, &rbClusterRole)

		By("Manually deleting a ClusterRoleBinding")
		err = k8sClient.Delete(ctx, &crb)
		Expect(err).NotTo(HaveOccurred())

		By("Eventually backfilling the ClusterRoleBinding")
		EventuallyGetObject(ctx, crbName, &crb)
	}, SpecTimeout(5*time.Second))

	It("Should set status conditions", func(ctx SpecContext) {
		imageRef := registry.MustUpsertTag("app8", "latest")
		app := makeApp("app8", imageRef)
		Expect(k8sClient.Create(ctx, &app)).Should(Succeed())

		By("Setting the conditions after creation")
		EventuallyHaveCondition(ctx, &app, "Ready", metav1.ConditionUnknown, "Reconciling")

		By("An issue creating an Ingress")
		EventuallyUpdateApp(ctx, &app, func() {
			app.Spec.Ingress.Host = "---"
		})

		By("Setting the Ready status condition to False")
		EventuallyHaveCondition(ctx, &app, "Ready", metav1.ConditionFalse, "IngressUpsertFailed")

		By("Fixing the ingress")
		EventuallyUpdateApp(ctx, &app, func() {
			app.Spec.Ingress.Host = "foo.example.com"
		})

		By("Deployment becoming Available")
		deployName := mkName(app.Namespace, app.Name)
		SetDeploymentAvailableStatus(ctx, deployName, true, "MinimumReplicasAvailable")

		By("Setting the Ready status condition to True")
		EventuallyHaveCondition(ctx, &app, "Ready", metav1.ConditionTrue, "MinimumReplicasAvailable")

		By("Deployment becoming unavailable")
		SetDeploymentAvailableStatus(ctx, deployName, false, "MinimumReplicasUnavailable")

		By("Setting the Ready status condition to False")
		EventuallyHaveCondition(ctx, &app, "Ready", metav1.ConditionFalse, "MinimumReplicasUnavailable")
	}, SpecTimeout(5*time.Second))

	It("Should create PodMonitor with defaults", func(ctx SpecContext) {
		imageRef := registry.MustUpsertTag("app9", "latest")
		app := makeApp("app9", imageRef)
		app.Spec.Ports = append(app.Spec.Ports, corev1.ContainerPort{
			Name:          "metrics",
			ContainerPort: 8192,
			Protocol:      corev1.ProtocolTCP,
		})
		app.Spec.Metrics = &yarotskymev1alpha1.Metrics{
			Enabled: true,
		}
		Expect(k8sClient.Create(ctx, &app)).Should(Succeed())

		By("Creating the PodMonitor")
		Eventually(func(g Gomega) {
			var pm prometheusv1.PodMonitor
			pmName := mkName(app.Namespace, app.Name)

			err := k8sClient.Get(ctx, pmName, &pm)
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(pm.Spec.PodMetricsEndpoints).To(ConsistOf(
				prometheusv1.PodMetricsEndpoint{
					Port: "metrics",
					Path: "/metrics",
				},
			))

			g.Expect(pm.Spec.Selector).To(Equal(
				metav1.LabelSelector{
					MatchLabels: map[string]string{
						"app.kubernetes.io/name":       app.Name,
						"app.kubernetes.io/managed-by": "application-controller",
						"app.kubernetes.io/instance":   "default",
						"app.kubernetes.io/version":    "0.1.0",
					},
				}))
		}).WithContext(ctx).Should(Succeed())
	}, SpecTimeout(5*time.Second))

	It("Should create PodMonitor with overrides", func(ctx SpecContext) {
		imageRef := registry.MustUpsertTag("app10", "latest")
		app := makeApp("app10", imageRef)
		app.Spec.Ports = append(app.Spec.Ports, corev1.ContainerPort{
			Name:          "mymetrics",
			ContainerPort: 8192,
			Protocol:      corev1.ProtocolTCP,
		})
		app.Spec.Metrics = &yarotskymev1alpha1.Metrics{
			Enabled:  true,
			PortName: "mymetrics",
			Path:     "/mymetrics",
		}
		Expect(k8sClient.Create(ctx, &app)).Should(Succeed())

		By("Creating the PodMonitor")
		Eventually(func(g Gomega) {
			var pm prometheusv1.PodMonitor
			pmName := mkName(app.Namespace, app.Name)

			err := k8sClient.Get(ctx, pmName, &pm)
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(pm.Spec.PodMetricsEndpoints).To(ConsistOf(
				prometheusv1.PodMetricsEndpoint{
					Port: "mymetrics",
					Path: "/mymetrics",
				},
			))
		}).WithContext(ctx).Should(Succeed())
	}, SpecTimeout(5*time.Second))
})

func EventuallyGetObject(ctx context.Context, name types.NamespacedName, obj client.Object) {
	GinkgoHelper()

	Eventually(func(g Gomega) {
		err := k8sClient.Get(ctx, name, obj)
		g.Expect(err).NotTo(HaveOccurred())
	}).WithContext(ctx).Should(Succeed())
}

func EventuallyNotFindObject(ctx context.Context, name types.NamespacedName, obj client.Object) {
	GinkgoHelper()

	Eventually(func(g Gomega) {
		err := k8sClient.Get(ctx, name, obj)
		g.Expect(errors.IsNotFound(err)).To(BeTrue())
	}).WithContext(ctx).Should(Succeed())
}

func EventuallyHaveCondition(ctx context.Context, app *yarotskymev1alpha1.Application, name string, status metav1.ConditionStatus, reason string) {
	GinkgoHelper()

	Eventually(func(g Gomega) {
		err := k8sClient.Get(ctx, mkName(app.Namespace, app.Name), app)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(app.Status.Conditions).To(ContainElement(
			SatisfyAll(
				HaveField("Type", name),
				HaveField("Status", status),
				HaveField("Reason", reason),
			),
		))
	}).WithContext(ctx).Should(Succeed())
}

func EventuallyUpdateApp(ctx context.Context, app *yarotskymev1alpha1.Application, mutateFn func()) {
	GinkgoHelper()

	Eventually(func(g Gomega) {
		err := k8sClient.Get(ctx, mkName(app.Namespace, app.Name), app)
		g.Expect(err).NotTo(HaveOccurred())

		mutateFn()
		err = k8sClient.Update(ctx, app)
		g.Expect(err).NotTo(HaveOccurred())
	}).WithContext(ctx).Should(Succeed())
}

func SetDeploymentAvailableStatus(ctx context.Context, deployName types.NamespacedName, available bool, reason string) {
	GinkgoHelper()

	var status corev1.ConditionStatus
	if available {
		status = corev1.ConditionTrue
	} else {
		status = corev1.ConditionFalse
	}

	Eventually(func(g Gomega) {
		var deploy appsv1.Deployment
		err := k8sClient.Get(ctx, deployName, &deploy)
		g.Expect(err).NotTo(HaveOccurred())

		deploy.Status.Conditions = []appsv1.DeploymentCondition{
			{
				Type:   appsv1.DeploymentAvailable,
				Status: status,
				Reason: reason,
			},
		}
		err = k8sClient.Status().Update(ctx, &deploy)
		g.Expect(err).NotTo(HaveOccurred())
	}).WithContext(ctx).Should(Succeed())
}

func mkName(namespace, name string) types.NamespacedName {
	return types.NamespacedName{
		Namespace: namespace,
		Name:      name,
	}
}
