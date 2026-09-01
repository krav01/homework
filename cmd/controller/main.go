package main

import (
	"crypto/tls"
	"flag"
	"log/slog"
	"os"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	sundayv1alpha1 "github.com/codex/sunday-system/api/v1alpha1"
	"github.com/codex/sunday-system/internal/controller"
)

func main() {
	var metricsAddress string
	var probeAddress string
	var leaderElection bool
	flag.StringVar(&metricsAddress, "metrics-bind-address", ":8080", "metrics server address")
	flag.StringVar(&probeAddress, "health-probe-bind-address", ":8081", "health probe address")
	flag.BoolVar(&leaderElection, "leader-elect", true, "enable leader election")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	ctrl.SetLogger(logr.FromSlogHandler(logger.Handler()))

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(sundayv1alpha1.AddToScheme(scheme))

	manager, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: server.Options{
			BindAddress: metricsAddress,
			TLSOpts: []func(*tls.Config){func(config *tls.Config) {
				config.MinVersion = tls.VersionTLS12
			}},
		},
		HealthProbeBindAddress: probeAddress,
		LeaderElection:         leaderElection,
		LeaderElectionID:       "etherealpod-controller.sunday.system",
	})
	if err != nil {
		logger.Error("create manager", "error", err)
		os.Exit(1)
	}

	reconciler := &controller.EtherealPodReconciler{Client: manager.GetClient(), Scheme: manager.GetScheme()}
	if err := reconciler.SetupWithManager(manager); err != nil {
		logger.Error("register controller", "error", err)
		os.Exit(1)
	}
	if err := manager.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		logger.Error("register health check", "error", err)
		os.Exit(1)
	}
	if err := manager.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		logger.Error("register readiness check", "error", err)
		os.Exit(1)
	}

	logger.Info("starting EtherealPod controller")
	if err := manager.Start(ctrl.SetupSignalHandler()); err != nil {
		logger.Error("run manager", "error", err)
		os.Exit(1)
	}
}
