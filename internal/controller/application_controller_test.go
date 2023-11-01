package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
)

var _ = Describe("Application controller", func() {
	const (
		namespace = "default"
	)

	Context("When setting up the test environment", func() {
		It("Should create Application custom resources", func() {
			By("Creating a first Application CR")
			ctx := context.Background()
			app1 := yarotskymev1alpha1.Application{
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
				},
			}
			Expect(k8sClient.Create(ctx, &app1)).Should(Succeed())
		})
	})
})
