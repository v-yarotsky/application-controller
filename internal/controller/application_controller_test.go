package controller

import (
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
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/event"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	"git.home.yarotsky.me/vlad/application-controller/internal/images"
	"git.home.yarotsky.me/vlad/application-controller/internal/testutil"
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
					VersionStrategy: "digest",
					Digest: yarotskymev1alpha1.VersionStrategyDigest{
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
		serviceAccountName := types.NamespacedName{
			Name:      app.Name,
			Namespace: app.Namespace,
		}
		Eventually(func(g Gomega) {
			var serviceAccount corev1.ServiceAccount
			err := k8sClient.Get(ctx, serviceAccountName, &serviceAccount)
			g.Expect(err).NotTo(HaveOccurred())
		}).WithContext(ctx).Should(Succeed())

		By("Creating cluster role bindings for ClusterRoles")
		Eventually(func(g Gomega) {
			var crb rbacv1.ClusterRoleBinding
			err := k8sClient.Get(ctx, types.NamespacedName{Name: "app1-clusterrole-my-cluster-role"}, &crb)
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
			err := k8sClient.Get(ctx, types.NamespacedName{Name: "app1-clusterrole-my-cluster-role-for-namespace", Namespace: app.Namespace}, &rb)
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
			err := k8sClient.Get(ctx, types.NamespacedName{Name: "app1-role-my-role", Namespace: app.Namespace}, &rb)
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
			err := k8sClient.Get(ctx, types.NamespacedName{Name: app.Name, Namespace: app.Namespace}, &deploy)
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(deploy.Spec.Template.Spec.ServiceAccountName).To(Equal(serviceAccountName.Name))
			g.Expect(deploy.Spec.Template.Spec.Containers).To(HaveLen(1))

			mainContainer := deploy.Spec.Template.Spec.Containers[0]
			g.Expect(mainContainer.Image).To(Equal(imageRef.String()))
			g.Expect(mainContainer.Ports).To(HaveLen(1))
			g.Expect(mainContainer.Ports).To(ContainElement(corev1.ContainerPort{ContainerPort: 8080, Name: "http", Protocol: corev1.ProtocolTCP}))
		}).WithContext(ctx).Should(Succeed())

		By("Creating a service")
		serviceName := types.NamespacedName{Name: app.Name, Namespace: app.Namespace}
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
			err := k8sClient.Get(ctx, types.NamespacedName{Name: app.Name, Namespace: app.Namespace}, &ingress)
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
		Expect(k8sClient.Create(ctx, &app)).Should(Succeed())

		deployName := types.NamespacedName{
			Name:      app.Name,
			Namespace: app.Namespace,
		}
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
	}, SpecTimeout(5*time.Second))

	It("Should use default ingress class when no explicit ingress class is specified", func(ctx SpecContext) {
		imageRef := registry.MustUpsertTag("app3", "latest")
		app := makeApp("app3", imageRef)
		app.Spec.Ingress.IngressClassName = nil

		Expect(k8sClient.Create(ctx, &app)).Should(Succeed())

		By("Creating an Ingress")
		Eventually(func(g Gomega) {
			var ingress networkingv1.Ingress
			err := k8sClient.Get(ctx, types.NamespacedName{Name: app.Name, Namespace: app.Namespace}, &ingress)
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(*ingress.Spec.IngressClassName).To(Equal("nginx-private"))
		}).WithContext(ctx).Should(Succeed())

	}, SpecTimeout(5*time.Second))

	It("Should properly delete created ClusterRoleBinding objects", func(ctx SpecContext) {
		imageRef := registry.MustUpsertTag("app4", "latest")
		app := makeApp("app4", imageRef)

		crbName := types.NamespacedName{
			Name: "app4-clusterrole-my-cluster-role",
		}
		var crb rbacv1.ClusterRoleBinding

		Expect(k8sClient.Create(ctx, &app)).Should(Succeed())

		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, crbName, &crb)
			g.Expect(err).NotTo(HaveOccurred())
		}).WithContext(ctx).Should(Succeed())

		By("Deleting the CluseterRoleBinding objects")
		Expect(k8sClient.Delete(ctx, &app)).Should(Succeed())

		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, crbName, &crb)
			g.Expect(errors.IsNotFound(err)).To(BeTrue())

		}).WithContext(ctx).Should(Succeed())

	}, SpecTimeout(5*time.Second))

	It("Should update the deployment when container configuration changes", func(ctx SpecContext) {
		imageRef := registry.MustUpsertTag("app5", "latest")
		app := makeApp("app5", imageRef)
		Expect(k8sClient.Create(ctx, &app)).Should(Succeed())

		deployName := types.NamespacedName{Name: app.Name, Namespace: app.Namespace}
		var deploy appsv1.Deployment

		By("Creating a deployment")
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, deployName, &deploy)
			g.Expect(err).NotTo(HaveOccurred())
		}).WithContext(ctx).Should(Succeed())

		By("Updating the Application")
		err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
			err := k8sClient.Get(ctx, types.NamespacedName{Name: app.Name, Namespace: app.Namespace}, &app)
			if err != nil {
				return err
			}
			app.Spec.Env = []corev1.EnvVar{
				{
					Name:  "MY_ENV_VAR",
					Value: "FOO",
				},
			}
			return k8sClient.Update(ctx, &app)
		})
		Expect(err).NotTo(HaveOccurred())

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
		rbName := types.NamespacedName{Name: "app6-role-my-role", Namespace: app.Namespace}
		rbClusterRoleName := types.NamespacedName{Name: "app6-clusterrole-my-cluster-role-for-namespace", Namespace: app.Namespace}
		crbName := types.NamespacedName{Name: "app6-clusterrole-my-cluster-role"}

		By("Creating the role bindings")
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, rbName, &rb)
			g.Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx, rbClusterRoleName, &rbClusterRole)
			g.Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx, crbName, &crb)
			g.Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx, app.NamespacedName(), &app)
			g.Expect(err).NotTo(HaveOccurred())
		}).WithContext(ctx).Should(Succeed())

		By("Updating the Application")
		err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
			err := k8sClient.Get(ctx, app.NamespacedName(), &app)
			if err != nil {
				return err
			}
			app.Spec.Roles = []yarotskymev1alpha1.ScopedRoleRef{}
			return k8sClient.Update(ctx, &app)
		})
		Expect(err).NotTo(HaveOccurred())

		By("Eventually removing the role bindings")
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, rbName, &rb)
			g.Expect(errors.IsNotFound(err)).To(BeTrue())

			err = k8sClient.Get(ctx, rbClusterRoleName, &rbClusterRole)
			g.Expect(errors.IsNotFound(err)).To(BeTrue())

			err = k8sClient.Get(ctx, crbName, &crb)
			g.Expect(errors.IsNotFound(err)).To(BeTrue())

			err = k8sClient.Get(ctx, app.NamespacedName(), &app)
			g.Expect(err).NotTo(HaveOccurred())
		}).WithContext(ctx).Should(Succeed())
	}, SpecTimeout(5*time.Second))

	It("Should backfill missing RoleBindings and ClusterRoleBindings", func(ctx SpecContext) {
		imageRef := registry.MustUpsertTag("app7", "latest")
		app := makeApp("app7", imageRef)
		Expect(k8sClient.Create(ctx, &app)).Should(Succeed())

		var rb, rbClusterRole rbacv1.RoleBinding
		var crb rbacv1.ClusterRoleBinding
		rbName := types.NamespacedName{Name: "app7-role-my-role", Namespace: app.Namespace}
		rbClusterRoleName := types.NamespacedName{Name: "app7-clusterrole-my-cluster-role-for-namespace", Namespace: app.Namespace}
		crbName := types.NamespacedName{Name: "app7-clusterrole-my-cluster-role"}

		By("Creating the role bindings")
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, rbName, &rb)
			g.Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx, rbClusterRoleName, &rbClusterRole)
			g.Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx, crbName, &crb)
			g.Expect(err).NotTo(HaveOccurred())
		}).WithContext(ctx).Should(Succeed())

		By("Manually deleting a RoleBinding")
		err := k8sClient.Delete(ctx, &rb)
		Expect(err).NotTo(HaveOccurred())

		By("Eventually backfilling the RoleBinding")
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, rbName, &rb)
			g.Expect(err).NotTo(HaveOccurred())
		}).WithContext(ctx).Should(Succeed())

		By("Manually deleting a RoleBinding for a ClusterRole")
		err = k8sClient.Delete(ctx, &rbClusterRole)
		Expect(err).NotTo(HaveOccurred())

		By("Eventually backfilling the RoleBinding for a ClusterRole")
		Eventually(func(g Gomega) {
			err = k8sClient.Get(ctx, rbClusterRoleName, &rbClusterRole)
			g.Expect(err).NotTo(HaveOccurred())
		}).WithContext(ctx).Should(Succeed())

		By("Manually deleting a ClusterRoleBinding")
		err = k8sClient.Delete(ctx, &crb)
		Expect(err).NotTo(HaveOccurred())

		By("Eventually backfilling the ClusterRoleBinding")
		Eventually(func(g Gomega) {
			err = k8sClient.Get(ctx, crbName, &crb)
			g.Expect(err).NotTo(HaveOccurred())
		}).WithContext(ctx).Should(Succeed())
	}, SpecTimeout(5*time.Second))
})
