// Package main implements the main entrypoint for the eks-webhook-proxy.
package main

import (
	"flag"
	"github.com/CharlieR-o-o-t/eks-webhook-proxy/controllers/endpointslice"
	"github.com/CharlieR-o-o-t/eks-webhook-proxy/controllers/mutating"
	"github.com/CharlieR-o-o-t/eks-webhook-proxy/controllers/validating"
	"github.com/CharlieR-o-o-t/eks-webhook-proxy/pkg/nodecache"
	"github.com/CharlieR-o-o-t/eks-webhook-proxy/pkg/proxy"
	"k8s.io/klog/v2"
	"os"

	"sigs.k8s.io/controller-runtime/pkg/log"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	crdcontroller "github.com/CharlieR-o-o-t/eks-webhook-proxy/controllers/crd"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	"github.com/spf13/pflag"

	"github.com/CharlieR-o-o-t/eks-webhook-proxy/config"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
)

var (
	scheme = runtime.NewScheme()

	metricsAddr          string
	enableLeaderElection bool
	probeAddr            string
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(scheme))
}

func initFlags(fs *pflag.FlagSet) {
	fs.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	fs.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
}

func main() {
	klog.InitFlags(nil)
	initFlags(pflag.CommandLine)
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	pflag.Parse()

	logger := klog.NewKlogr().WithName("eks-webhook-proxy")

	cfg, err := config.New()
	if err != nil {
		logger.Error(err, "failed to load config")
		os.Exit(1)
	}

	ctrl.SetLogger(logger)

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "3bf63fmi.k8s.gw.tc",
	})
	if err != nil {
		logger.Error(err, "unable to start manager")
		os.Exit(1)
	}

	nodeCache := nodecache.NewNodeIPCache()
	if err := nodecache.SetupNodeWatch(mgr, nodeCache); err != nil {
		logger.Error(err, "failed to setup node cache")
		os.Exit(1)
	}

	proxyHandler := proxy.New(mgr.GetClient(), cfg, nodeCache)

	if err := (&crdcontroller.Controller{
		Config: cfg,
		Proxy:  proxyHandler,
		Client: mgr.GetClient(),
		Log:    log.Log.WithName(crdcontroller.ControllerName),
	}).SetupWithManager(mgr); err != nil {
		logger.Error(err, "failed to setup CRD controller")
		os.Exit(1)
	}

	if err := (&endpointslice.Controller{
		Config: cfg,
		Proxy:  proxyHandler,
		Client: mgr.GetClient(),
		Log:    log.Log.WithName(endpointslice.ControllerName),
	}).SetupWithManager(mgr); err != nil {
		logger.Error(err, "failed to setup endpoint slice controller")
		os.Exit(1)
	}

	if err := (&mutating.Controller{
		Config: cfg,
		Proxy:  proxyHandler,
		Client: mgr.GetClient(),
		Log:    log.Log.WithName(mutating.ControllerName),
	}).SetupWithManager(mgr); err != nil {
		logger.Error(err, "failed to setup mutating controller")
		os.Exit(1)
	}

	if err := (&validating.Controller{
		Config: cfg,
		Proxy:  proxyHandler,
		Client: mgr.GetClient(),
		Log:    log.Log.WithName(validating.ControllerName),
	}).SetupWithManager(mgr); err != nil {
		logger.Error(err, "failed to setup validating controller")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		logger.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		logger.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	logger.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		logger.Error(err, "problem running manager")
		os.Exit(1)
	}
}
