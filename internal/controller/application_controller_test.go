package controller

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
)

var _ = Describe("Application controller", func() {
	const (
		namespace = "default"
	)

	Context("When setting up the test environment", func() {
		It("Should create Application custom resources", func(ctx SpecContext) {
			By("Creating a first Application CR")
			app := yarotskymev1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "app1",
					Namespace: namespace,
				},
				Spec: yarotskymev1alpha1.ApplicationSpec{
					Image: yarotskymev1alpha1.ImageSpec{
						Repository:      "git.home.yarotsky.me/vlad/dashboard",
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
				},
			}
			Expect(k8sClient.Create(ctx, &app)).Should(Succeed())

			By("Creating a service account")
			serviceAccountName := types.NamespacedName{
				Name:      app.Name,
				Namespace: app.Namespace,
			}
			var serviceAccount corev1.ServiceAccount
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, serviceAccountName, &serviceAccount)
				g.Expect(err).NotTo(HaveOccurred())
			}).WithContext(ctx).Should(Succeed())

			By("Creating a deployment")
			deployName := types.NamespacedName{
				Name:      app.Name,
				Namespace: app.Namespace,
			}
			var deploy appsv1.Deployment
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, deployName, &deploy)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(deploy.Spec.Template.Spec.ServiceAccountName).To(Equal(serviceAccountName.Name))
				g.Expect(deploy.Spec.Template.Spec.Containers).To(HaveLen(1))

				mainContainer := deploy.Spec.Template.Spec.Containers[0]
				g.Expect(mainContainer.Image).To(Equal("git.home.yarotsky.me/vlad/dashboard:v0.1.0"))
				g.Expect(mainContainer.Ports).To(HaveLen(1))
				g.Expect(mainContainer.Ports).To(ContainElement(corev1.ContainerPort{ContainerPort: 8080, Name: "http", Protocol: corev1.ProtocolTCP}))
			}).WithContext(ctx).Should(Succeed())

			By("Creating a service")
			serviceName := types.NamespacedName{
				Name:      app.Name,
				Namespace: app.Namespace,
			}
			var service corev1.Service
			Eventually(func(g Gomega) {
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
			ingressName := types.NamespacedName{
				Name:      app.Name,
				Namespace: app.Namespace,
			}
			var ingress networkingv1.Ingress
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, ingressName, &ingress)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(*ingress.Spec.IngressClassName).To(Equal("traefik"))
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
		}, SpecTimeout(10*time.Second))
	})
})
