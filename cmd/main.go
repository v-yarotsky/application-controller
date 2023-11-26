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

package main

import (
	"flag"
	"os"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.

	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	yarotskymev1alpha1 "git.home.yarotsky.me/vlad/application-controller/api/v1alpha1"
	"git.home.yarotsky.me/vlad/application-controller/internal/controller"
	flagext "git.home.yarotsky.me/vlad/application-controller/internal/flag"
	"git.home.yarotsky.me/vlad/application-controller/internal/images"
	"git.home.yarotsky.me/vlad/application-controller/internal/k8s"
	prometheusv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	traefikv1alpha1 "github.com/traefik/traefik/v2/pkg/provider/kubernetes/crd/traefikio/v1alpha1"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(prometheusv1.AddToScheme(scheme))
	utilruntime.Must(traefikv1alpha1.AddToScheme(scheme))
	utilruntime.Must(yarotskymev1alpha1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var imagePullSecrets flagext.StringSlice
	var traefikMiddlewares flagext.StringSlice
	var defaultUpdateSchedule flagext.CronSchedule = "*/5 * * * * *"

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.Var(&imagePullSecrets, "image-pull-secret", "name of a Secret with image registry credentials. Can be used multiple times.")
	flag.Var(&traefikMiddlewares, "traefik-default-middleware", "Traefik middleware namespace/name that is added to IngressRoutes by default, e.g. "+
		"`kube-system/https-redirect` Can be used multiple times.")
	flag.Var(&defaultUpdateSchedule, "default-update-schedule", "Default Cron schedule for image update checks (default: `@every 5m`);"+
		" See https://pkg.go.dev/github.com/robfig/cron#hdr-CRON_Expression_Format")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	hasPrometheus, err := k8s.IsPrometheusOperatorInstalled(nil)
	if err != nil {
		setupLog.Error(err, "failed to check presence of prometheus API types")
		os.Exit(1)
	}
	if !hasPrometheus {
		setupLog.Info("Prometheus operator does not appear to be installed (or the corresponding CRDs are missing). Monitoring will not be automatically setup")
	}

	hasTraefik, err := k8s.IsTraefikInstalled(nil)
	if err != nil {
		setupLog.Error(err, "failed to check presence of Traefik API types")
		os.Exit(1)
	}
	if !hasTraefik {
		setupLog.Info("Traefik does not appear to be installed (or the corresponding CRDs are missing). Ingress will not be automatically setup")
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "32c0c598.yarotsky.me",
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	imageWatcher, err := images.NewCronImageWatcherWithDefaults(
		mgr.GetClient(),
		defaultUpdateSchedule.Get(),
		imagePullSecrets,
		30*time.Second,
	)

	if err != nil {
		setupLog.Error(err, "failed to instantiate image watcher")
		os.Exit(1)
	}

	imageUpdateEvents := make(chan event.GenericEvent)

	if err = (&controller.ApplicationReconciler{
		Client:                    mgr.GetClient(),
		Scheme:                    mgr.GetScheme(),
		ImageFinder:               imageWatcher,
		ImageUpdateEvents:         imageUpdateEvents,
		DefaultTraefikMiddlewares: traefikMiddlewares,
		SupportsTraefik:           hasTraefik,
		SupportsPrometheus:        hasPrometheus,
		Recorder:                  mgr.GetEventRecorderFor(controller.Name),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Application")
		os.Exit(1)
	}
	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	ctx := ctrl.SetupSignalHandler()

	setupLog.Info("starting application update watcher")
	go imageWatcher.WatchForNewImages(ctx, imageUpdateEvents)

	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
