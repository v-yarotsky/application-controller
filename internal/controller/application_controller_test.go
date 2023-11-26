package controller

import (
	"context"
	"fmt"
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
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	"git.home.yarotsky.me/vlad/application-controller/internal/images"
	"git.home.yarotsky.me/vlad/application-controller/internal/k8s"
	"git.home.yarotsky.me/vlad/application-controller/internal/testutil"
	prometheusv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	traefikv1alpha1 "github.com/traefik/traefik/v2/pkg/provider/kubernetes/crd/traefikcontainous/v1alpha1"
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
					IngressClassName: ptr.To("traefik"),
					Host:             "dashboard.home.yarotsky.me",
				},
				Ingress2: &yarotskymev1alpha1.Ingress{
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
		var crb rbacv1.ClusterRoleBinding
		EventuallyGetObject(ctx, mkName("", "default-app1-clusterrole-my-cluster-role"), &crb, func(g Gomega) {
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
		})

		By("Creating role bindings for ClusterRoles")
		var rb rbacv1.RoleBinding
		EventuallyGetObject(ctx, mkName(app.Namespace, "app1-clusterrole-my-cluster-role-for-namespace"), &rb, func(g Gomega) {
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
		})

		By("Creating role bindings for Roles")
		var rb2 rbacv1.RoleBinding
		EventuallyGetObject(ctx, mkName(app.Namespace, "app1-role-my-role"), &rb2, func(g Gomega) {
			g.Expect(rb2.Subjects).To(ContainElement(rbacv1.Subject{
				APIGroup:  "",
				Kind:      "ServiceAccount",
				Name:      serviceAccountName.Name,
				Namespace: serviceAccountName.Namespace,
			}))

			g.Expect(rb2.RoleRef).To(Equal(rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "Role",
				Name:     "my-role",
			}))
		})

		By("Creating a deployment")
		var deploy appsv1.Deployment
		EventuallyGetObject(ctx, mkName(app.Namespace, app.Name), &deploy, func(g Gomega) {
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
			g.Expect(mainContainer.Ports).To(HaveLen(3))
			g.Expect(mainContainer.Ports).To(ContainElements(
				SatisfyAll(
					HaveField("Name", "http"),
					HaveField("ContainerPort", int32(8080)),
					HaveField("Protocol", corev1.ProtocolTCP),
				),
				SatisfyAll(
					HaveField("Name", "metrics"),
					HaveField("ContainerPort", int32(8192)),
					HaveField("Protocol", corev1.ProtocolTCP),
				),
				SatisfyAll(
					HaveField("Name", "listen-udp"),
					HaveField("ContainerPort", int32(22000)),
					HaveField("Protocol", corev1.ProtocolUDP),
				),
			))
			g.Expect(mainContainer.VolumeMounts).To(ContainElement(corev1.VolumeMount{
				Name:      "data",
				MountPath: "/data",
			}))
		})

		By("Creating a service")
		serviceName := mkName(app.Namespace, app.Name)
		var service corev1.Service
		EventuallyGetObject(ctx, serviceName, &service, func(g Gomega) {
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
			}))
		})

		By("Creating an Ingress")
		var ingress networkingv1.Ingress
		EventuallyGetObject(ctx, mkName(app.Namespace, app.Name), &ingress, func(g Gomega) {
			g.Expect(*ingress.Spec.IngressClassName).To(Equal("traefik"))
			g.Expect(ingress.Annotations).To(Equal(map[string]string{
				"traefik.ingress.kubernetes.io/router.middlewares": "kube-system-foo@kubernetescrd",
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
		})

		By("Creating an IngressRoute")
		var ingressRoute traefikv1alpha1.IngressRoute
		EventuallyGetObject(ctx, mkName(app.Namespace, app.Name), &ingressRoute, func(g Gomega) {
			g.Expect(ingressRoute.Spec.Routes).To(ContainElement(traefikv1alpha1.Route{
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
			}))
		})

		By("Creating a LoadBalancer service")
		lbServiceName := mkName(app.Namespace, fmt.Sprintf("%s-loadbalancer", app.Name))
		var lbService corev1.Service
		EventuallyGetObject(ctx, lbServiceName, &lbService, func(g Gomega) {
			g.Expect(lbService.Annotations).To(HaveKeyWithValue(
				"external-dns.alpha.kubernetes.io/hostname",
				"endpoint.home.yarotsky.me",
			))

			g.Expect(lbService.Spec.Ports).To(ContainElement(
				SatisfyAll(
					HaveField("Name", "listen-udp"),
					HaveField("TargetPort", intstr.FromString("listen-udp")),
					HaveField("Protocol", corev1.ProtocolUDP),
					HaveField("Port", int32(22000)),
				),
			))

			g.Expect(lbService.Spec.Selector).To(Equal(map[string]string{
				"app.kubernetes.io/name":       app.Name,
				"app.kubernetes.io/managed-by": "application-controller",
				"app.kubernetes.io/instance":   "default",
			}))
		})

		By("Creating the PodMonitor")
		var pm prometheusv1.PodMonitor
		pmName := mkName(app.Namespace, app.Name)
		EventuallyGetObject(ctx, pmName, &pm, func(g Gomega) {
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
					},
				}))
		})
	}, SpecTimeout(5*time.Second))

	It("Should update managed deployments when a new image is available", func(ctx SpecContext) {
		imageRef := registry.MustUpsertTag("app2", "latest")
		app := makeApp("app2", imageRef)
		appName := mkName(app.Namespace, app.Name)
		Expect(k8sClient.Create(ctx, &app)).Should(Succeed())

		deployName := mkName(app.Namespace, app.Name)
		var deploy appsv1.Deployment

		By("Creating a deployment with the current image")
		EventuallyGetObject(ctx, deployName, &deploy, func(g Gomega) {
			g.Expect(deploy.Spec.Template.Spec.Containers).To(HaveLen(1))

			mainContainer := deploy.Spec.Template.Spec.Containers[0]
			g.Expect(mainContainer.Image).To(Equal(imageRef.String()))
		})

		By("Eventually updating the image")
		newImageDigestRef := registry.MustUpsertTag("app2", "latest")
		imageUpdateEvents <- event.GenericEvent{Object: &app}

		EventuallyGetObject(ctx, deployName, &deploy, func(g Gomega) {
			g.Expect(deploy.Spec.Template.Spec.Containers).To(HaveLen(1))

			mainContainer := deploy.Spec.Template.Spec.Containers[0]
			g.Expect(mainContainer.Image).To(Equal(newImageDigestRef.String()))
		})

		EventuallyGetObject(ctx, appName, &app, func(g Gomega) {
			g.Expect(app.Status.Image).To(Equal(newImageDigestRef.String()))
		})
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
		EventuallyGetObject(ctx, deployName, &deploy, func(g Gomega) {
			g.Expect(deploy.Spec.Template.Spec.Containers).To(HaveLen(1))

			mainContainer := deploy.Spec.Template.Spec.Containers[0]
			g.Expect(mainContainer.Env).To(ContainElement(corev1.EnvVar{Name: "MY_ENV_VAR", Value: "FOO"}))
		})
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

	It("Should create PodMonitor with overrides", func(ctx SpecContext) {
		imageRef := registry.MustUpsertTag("app9", "latest")
		app := makeApp("app9", imageRef)
		app.Spec.Metrics = &yarotskymev1alpha1.Metrics{
			Enabled:  true,
			PortName: "http",
			Path:     "/mymetrics",
		}
		Expect(k8sClient.Create(ctx, &app)).Should(Succeed())

		By("Creating the PodMonitor")
		var pm prometheusv1.PodMonitor
		pmName := mkName(app.Namespace, app.Name)
		EventuallyGetObject(ctx, pmName, &pm, func(g Gomega) {
			g.Expect(pm.Spec.PodMetricsEndpoints).To(ConsistOf(
				prometheusv1.PodMetricsEndpoint{
					Port: "http",
					Path: "/mymetrics",
				},
			))
		})
	}, SpecTimeout(5*time.Second))

	It("Should remove PodMonitor when disabled", func(ctx SpecContext) {
		imageRef := registry.MustUpsertTag("app10", "latest")
		app := makeApp("app10", imageRef)
		Expect(k8sClient.Create(ctx, &app)).Should(Succeed())

		name := mkName(app.Namespace, app.Name)
		var mon prometheusv1.PodMonitor

		By("Creating the PodMonitor")
		EventuallyGetObject(ctx, name, &mon)

		By("Updating the Application")
		EventuallyUpdateApp(ctx, &app, func() {
			app.Spec.Metrics.Enabled = false
		})

		By("Eventually removing the PodMonitor")
		EventuallyNotFindObject(ctx, name, &mon)
	}, SpecTimeout(5*time.Second))

	It("Should determine whether Prometheus is supported", func(ctx SpecContext) {
		hasPrometheus, err := k8s.IsPrometheusOperatorInstalled(cfg)
		Expect(err).NotTo(HaveOccurred())
		Expect(hasPrometheus).To(BeTrue())
	}, SpecTimeout(5*time.Second))

	It("Should remove ingress when disabled", func(ctx SpecContext) {
		imageRef := registry.MustUpsertTag("app11", "latest")
		app := makeApp("app11", imageRef)
		Expect(k8sClient.Create(ctx, &app)).Should(Succeed())

		name := mkName(app.Namespace, app.Name)
		var ing networkingv1.Ingress

		By("Creating the Ingress")
		EventuallyGetObject(ctx, name, &ing)

		By("Updating the Application")
		EventuallyUpdateApp(ctx, &app, func() {
			app.Spec.Ingress = nil
		})

		By("Eventually removing the ingress")
		EventuallyNotFindObject(ctx, name, &ing)
	}, SpecTimeout(5*time.Second))

	It("Should remove Load Balancer service when disabled", func(ctx SpecContext) {
		imageRef := registry.MustUpsertTag("app12", "latest")
		app := makeApp("app12", imageRef)
		Expect(k8sClient.Create(ctx, &app)).Should(Succeed())

		name := mkName(app.Namespace, fmt.Sprintf("%s-loadbalancer", app.Name))
		var svc corev1.Service

		By("Creating the LoadBalancer Service")
		EventuallyGetObject(ctx, name, &svc)

		By("Updating the Application")
		EventuallyUpdateApp(ctx, &app, func() {
			app.Spec.LoadBalancer = nil
		})

		By("Eventually removing the LoadBalancer Service")
		EventuallyNotFindObject(ctx, name, &svc)
	}, SpecTimeout(5*time.Second))
})

func EventuallyGetObject(ctx context.Context, name types.NamespacedName, obj client.Object, matchFns ...func(g Gomega)) {
	GinkgoHelper()

	Eventually(func(g Gomega) {
		err := k8sClient.Get(ctx, name, obj)
		g.Expect(err).NotTo(HaveOccurred())

		for _, f := range matchFns {
			f(g)
		}
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
