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

package controller_test

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	ctrl "sigs.k8s.io/controller-runtime"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	"git.home.yarotsky.me/vlad/application-controller/internal/controller"
	"git.home.yarotsky.me/vlad/application-controller/internal/images"
	"git.home.yarotsky.me/vlad/application-controller/internal/testutil"
	prometheusv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	traefikv1alpha1 "github.com/traefik/traefik/v2/pkg/provider/kubernetes/crd/traefikio/v1alpha1"
	//+kubebuilder:scaffold:imports
)

const (
	namespace = "default"
)

var (
	cfg               *rest.Config
	k8sClient         client.Client
	testEnv           *envtest.Environment
	ctx               context.Context
	cancel            context.CancelFunc
	imageUpdateEvents chan event.GenericEvent
	registry          *testutil.TestRegistry
)

func initializeTestEnvironment(t *testing.T) {
	logf.SetLogger(zap.New(zap.UseDevMode(true)))
	ctx, cancel = context.WithCancel(context.TODO())

	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "config", "crd", "bases"),
			filepath.Join("..", "..", "hack", "external-crds"),
		},
		ErrorIfCRDPathMissing: true,

		// The BinaryAssetsDirectory is only required if you want to run the tests directly
		// without call the makefile target test. If not informed it will look for the
		// default path defined in controller-runtime which is /usr/local/kubebuilder/.
		// Note that you must have the required binaries setup under the bin directory to perform
		// the tests directly. When we run make test it will be setup and used automatically.
		BinaryAssetsDirectory: filepath.Join("..", "..", "bin", "k8s",
			fmt.Sprintf("1.28.0-%s-%s", runtime.GOOS, runtime.GOARCH)),
	}

	registry = testutil.NewTestRegistry(t)

	var err error
	// cfg is defined in this file globally.
	cfg, err = testEnv.Start()
	assert.NoError(t, err)
	assert.NotNil(t, cfg)

	err = yarotskymev1alpha1.AddToScheme(scheme.Scheme)
	assert.NoError(t, err)

	err = prometheusv1.AddToScheme(scheme.Scheme)
	assert.NoError(t, err)

	err = traefikv1alpha1.AddToScheme(scheme.Scheme)
	assert.NoError(t, err)

	//+kubebuilder:scaffold:scheme

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	assert.NoError(t, err)
	assert.NotNil(t, k8sClient)

	k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
	})
	assert.NoError(t, err)

	watcher, err := images.NewCronImageWatcherWithDefaults(k8sManager.GetClient(), "@every 500ms", nil, 500*time.Millisecond)
	assert.NoError(t, err)

	imageUpdateEvents = make(chan event.GenericEvent)
	err = (&controller.ApplicationReconciler{
		Client:                    k8sManager.GetClient(),
		Scheme:                    k8sManager.GetScheme(),
		ImageFinder:               watcher,
		ImageUpdateEvents:         imageUpdateEvents,
		DefaultTraefikMiddlewares: []types.NamespacedName{{Namespace: "kube-system", Name: "foo"}},
		TraefikCNAMETarget:        "traefik.example.com",
		AuthConfig: controller.AuthConfig{
			AuthPathPrefix:      "/oauth2/",
			AuthServiceName:     types.NamespacedName{Namespace: "kube-system", Name: "oauth2-proxy"},
			AuthServicePortName: "http",
			AuthMiddlewareName:  types.NamespacedName{Namespace: "kube-system", Name: "oauth2"},
		},
		Recorder:           k8sManager.GetEventRecorderFor(controller.Name),
		SupportsPrometheus: true,
		SupportsTraefik:    true,
	}).SetupWithManager(k8sManager)
	assert.NoError(t, err)

	ctxWithLog := log.IntoContext(ctx, logf.Log)
	go watcher.WatchForNewImages(ctxWithLog, imageUpdateEvents)

	go func() {
		err = k8sManager.Start(ctx)
		assert.NoError(t, err)
	}()
}

func tearDownTestEnvironment(t *testing.T) {
	cancel()
	err := testEnv.Stop()
	registry.Close()
	assert.NoError(t, err)
}

func EventuallyGetObject(t *testing.T, name types.NamespacedName, obj client.Object, matchFns ...func(t require.TestingT)) {
	t.Helper()

	eventually(t, func(t *assert.CollectT) {
		err := k8sClient.Get(context.TODO(), name, obj)
		assert.NoError(t, err)

		for _, f := range matchFns {
			f(t)
		}
	})
}

func EventuallyNotFindObject(t *testing.T, name types.NamespacedName, obj client.Object) {
	t.Helper()

	eventually(t, func(t *assert.CollectT) {
		err := k8sClient.Get(ctx, name, obj)
		assert.True(t, errors.IsNotFound(err))
	})
}

func EventuallyHaveCondition(t *testing.T, app *yarotskymev1alpha1.Application, name string, status metav1.ConditionStatus, reason string) {
	t.Helper()

	eventually(t, func(t *assert.CollectT) {
		err := k8sClient.Get(ctx, mkName(app.Namespace, app.Name), app)
		require.NoError(t, err)
		assert.True(t, slices.ContainsFunc(app.Status.Conditions, func(c metav1.Condition) bool {
			return c.Type == name &&
				c.Status == status &&
				c.Reason == reason
		}))
	})
}

func EventuallyUpdateApp(t *testing.T, app *yarotskymev1alpha1.Application, mutateFn func()) {
	t.Helper()

	eventually(t, func(t *assert.CollectT) {
		err := k8sClient.Get(ctx, mkName(app.Namespace, app.Name), app)
		require.NoError(t, err)

		mutateFn()
		err = k8sClient.Update(ctx, app)
		require.NoError(t, err)
	})
}

func SetDeploymentAvailableStatus(t *testing.T, deployName types.NamespacedName, available bool, reason string) {
	t.Helper()

	var status corev1.ConditionStatus
	if available {
		status = corev1.ConditionTrue
	} else {
		status = corev1.ConditionFalse
	}

	eventually(t, func(t *assert.CollectT) {
		var deploy appsv1.Deployment
		err := k8sClient.Get(ctx, deployName, &deploy)
		require.NoError(t, err)

		deploy.Status.Conditions = []appsv1.DeploymentCondition{
			{
				Type:   appsv1.DeploymentAvailable,
				Status: status,
				Reason: reason,
			},
		}
		err = k8sClient.Status().Update(ctx, &deploy)
		require.NoError(t, err)
	})
}

func eventually(t *testing.T, f func(t *assert.CollectT)) {
	assert.EventuallyWithT(t, f, 3*time.Second, 10*time.Millisecond)
}

func mkName(namespace, name string) types.NamespacedName {
	return types.NamespacedName{
		Namespace: namespace,
		Name:      name,
	}
}
