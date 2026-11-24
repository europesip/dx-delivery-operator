// SPDX-License-Identifier: MIT

/*
Copyright 2026 EUROPESIP.

Licensed under the MIT License (see LICENSE).
*/

package main

import (
	"time"
	"crypto/tls"
	"flag"
	"os"
	"path/filepath"
	"strings"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	ctrlwebhook "sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	dxv1alpha1 "github.com/europesip/dx-delivery-operator/api/v1alpha1"
	"github.com/europesip/dx-delivery-operator/internal/controller"
	"github.com/europesip/dx-delivery-operator/internal/dx"
	"github.com/europesip/dx-delivery-operator/internal/javaext"
	"github.com/europesip/dx-delivery-operator/internal/source"
	dxwebhook "github.com/europesip/dx-delivery-operator/internal/webhook"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(dxv1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

// nolint:gocyclo
func main() {
	var metricsAddr string
	var metricsCertPath, metricsCertName, metricsCertKey string
	var webhookCertPath, webhookCertName, webhookCertKey string
	var webhookPort int
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var tlsOpts []func(*tls.Config)
	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.StringVar(&webhookCertPath, "webhook-cert-path", "", "The directory that contains the webhook certificate.")
	flag.StringVar(&webhookCertName, "webhook-cert-name", "tls.crt", "The name of the webhook certificate file.")
	flag.StringVar(&webhookCertKey, "webhook-cert-key", "tls.key", "The name of the webhook key file.")
	flag.IntVar(&webhookPort, "webhook-port", 9443, "Port the webhook server listens on. "+
		"Defaults to 9443. Set -1 to disable the webhook server.")
	flag.StringVar(&metricsCertPath, "metrics-cert-path", "",
		"The directory that contains the metrics server certificate.")
	flag.StringVar(&metricsCertName, "metrics-cert-name", "tls.crt", "The name of the metrics server certificate file.")
	flag.StringVar(&metricsCertKey, "metrics-cert-key", "tls.key", "The name of the metrics server key file.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("Disabling HTTP/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	// Initial webhook TLS options
	webhookTLSOpts := tlsOpts
	webhookServerOptions := ctrlwebhook.Options{
		TLSOpts: webhookTLSOpts,
		Port:    webhookPort,
	}

	if len(webhookCertPath) > 0 {
		setupLog.Info("Initializing webhook certificate watcher using provided certificates",
			"webhook-cert-path", webhookCertPath, "webhook-cert-name", webhookCertName, "webhook-cert-key", webhookCertKey)

		webhookServerOptions.CertDir = webhookCertPath
		webhookServerOptions.CertName = webhookCertName
		webhookServerOptions.KeyName = webhookCertKey
	}

	webhookServer := ctrlwebhook.NewServer(webhookServerOptions)

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.25.0/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
	}

	if secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.25.0/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	// If the certificate is not specified, controller-runtime will automatically
	// generate self-signed certificates for the metrics server. While convenient for development and testing,
	// this setup is not recommended for production.
	//
	// TODO(user): If you enable certManager, uncomment the following lines:
	// - [METRICS-WITH-CERTS] at config/default/kustomization.yaml to generate and use certificates
	// managed by cert-manager for the metrics server.
	// - [PROMETHEUS-WITH-CERTS] at config/prometheus/kustomization.yaml for TLS certification.
	if len(metricsCertPath) > 0 {
		setupLog.Info("Initializing metrics certificate watcher using provided certificates",
			"metrics-cert-path", metricsCertPath, "metrics-cert-name", metricsCertName, "metrics-cert-key", metricsCertKey)

		metricsServerOptions.CertDir = metricsCertPath
		metricsServerOptions.CertName = metricsCertName
		metricsServerOptions.KeyName = metricsCertKey
	}

	// WATCH_NAMESPACES (comma separated, Helm value watchNamespaces): only these namespaces. Empty: all.
	cacheOpts := cache.Options{}
	if ns := strings.TrimSpace(os.Getenv("WATCH_NAMESPACES")); ns != "" {
		cacheOpts.DefaultNamespaces = map[string]cache.Config{}
		for _, n := range strings.Split(ns, ",") {
			if n = strings.TrimSpace(n); n != "" {
				cacheOpts.DefaultNamespaces[n] = cache.Config{}
			}
		}
	}
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Cache:                  cacheOpts,
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       leaderElectionID(),
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
		setupLog.Error(err, "Failed to start manager")
		os.Exit(1)
	}

	// What every reconciler shares: DXClient (in the image, /dxclient), the source fetcher and the REST sessions
	workDir := os.Getenv("WORK_DIR")
	if workDir == "" {
		workDir = filepath.Join(os.TempDir(), "dxop")
	}
	// Events are also written as log lines only when asked (LOG_EVENTS=true, chart value logEvents): clusters that ship
	// Kubernetes events to their log stack would otherwise get every event twice.
	var eventRecorder = mgr.GetEventRecorder("dx-delivery-operator")
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("LOG_EVENTS"))); v == "true" || v == "1" {
		eventRecorder = controller.LoggingRecorder(eventRecorder, ctrl.Log.WithName("events"))
		setupLog.Info("every Kubernetes event is also written as a log line (LOG_EVENTS=true)")
	}
	shared := &controller.Shared{
		DXClient: dx.NewDXClientFromEnv(),
		Fetcher:  &source.Fetcher{Client: mgr.GetClient(), CacheDir: filepath.Join(workDir, "cache")},
		WorkDir:  workDir,
		Recorder: eventRecorder,
	}
	if err := shared.DXClient.Available(); err != nil {
		setupLog.Error(err, "DXClient cannot run: Script Applications will not be pushed until this is fixed")
	}

	// ENABLE_CONTROLLERS (comma separated, Helm value enableControllers): only these controllers run, for example a
	// second instance of the operator that only handles dxjavaextension in a test namespace. Empty: all.
	enabled := enabledControllers()

	if enabled("dxconnection") {
		if err := (&controller.DXConnectionReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
			Shared: shared,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "Failed to create controller", "controller", "dxconnection")
			os.Exit(1)
		}
	}
	if enabled("dxscriptapplication") {
		if err := (&controller.DXScriptApplicationReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
			Shared: shared,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "Failed to create controller", "controller", "dxscriptapplication")
			os.Exit(1)
		}
	}
	if enabled("dxpage") {
		if err := (&controller.DXPageReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
			Shared: shared,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "Failed to create controller", "controller", "dxpage")
			os.Exit(1)
		}
	}
	if enabled("dxxmlaccess") {
		if err := (&controller.DXXMLAccessReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
			Shared: shared,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "Failed to create controller", "controller", "dxxmlaccess")
			os.Exit(1)
		}
	}
	if enabled("dxtheme") {
		if err := (&controller.DXThemeReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
			Shared: shared,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "Failed to create controller", "controller", "dxtheme")
			os.Exit(1)
		}
	}
	if enabled("dxdamcollection") {
		if err := (&controller.DXDamCollectionReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
			Shared: shared,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "Failed to create controller", "controller", "dxdamcollection")
			os.Exit(1)
		}
	}
	if enabled("dxwcmlibraryimport") {
		if err := (&controller.DXWCMLibraryImportReconciler{
			Client:    mgr.GetClient(),
			APIReader: mgr.GetAPIReader(),
			Scheme:    mgr.GetScheme(),
			Shared:    shared,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "Failed to create controller", "controller", "dxwcmlibraryimport")
			os.Exit(1)
		}
	}
	if enabled("dxexport") {
		if err := (&controller.DXExportReconciler{
			Client:    mgr.GetClient(),
			APIReader: mgr.GetAPIReader(),
			Scheme:    mgr.GetScheme(),
			Shared:    shared,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "Failed to create controller", "controller", "dxexport")
			os.Exit(1)
		}
	}
	if enabled("dxwcmlibraryexport") {
		if err := (&controller.DXWCMLibraryExportReconciler{
			Client:    mgr.GetClient(),
			APIReader: mgr.GetAPIReader(),
			Scheme:    mgr.GetScheme(),
			Shared:    shared,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "Failed to create controller", "controller", "dxwcmlibraryexport")
			os.Exit(1)
		}
	}
	if enabled("dxwcmadmintask") {
		if err := (&controller.DXWCMAdminTaskReconciler{
			Client:    mgr.GetClient(),
			APIReader: mgr.GetAPIReader(),
			Scheme:    mgr.GetScheme(),
			Shared:    shared,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "Failed to create controller", "controller", "dxwcmadmintask")
			os.Exit(1)
		}
	}
	if enabled("dxdatabase") {
		if err := (&controller.DXDatabaseReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
			Shared: shared,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "Failed to create controller", "controller", "dxdatabase")
			os.Exit(1)
		}
	}
	if enabled("dxjavaextension") {
		// The default rollout timeout of DXJavaExtension (chart javaExtensions.rolloutTimeout, e.g. 30m).
		var jxTimeout time.Duration
		if v := os.Getenv("JAVA_EXTENSIONS_ROLLOUT_TIMEOUT"); v != "" {
			d, err := time.ParseDuration(v)
			if err != nil {
				setupLog.Error(err, "invalid JAVA_EXTENSIONS_ROLLOUT_TIMEOUT, using the default", "value", v)
			} else {
				jxTimeout = d
			}
		}
		if err := (&controller.DXJavaExtensionReconciler{
			Client:         mgr.GetClient(),
			APIReader:      mgr.GetAPIReader(),
			Scheme:         mgr.GetScheme(),
			Shared:         shared,
			RolloutTimeout: jxTimeout,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "Failed to create controller", "controller", "dxjavaextension")
			os.Exit(1)
		}
	}
	// The admission webhook of DXJavaExtension (chart value javaExtensions.enabled): injects the init container that
	// pulls the Java extensions into the Web Engine pods (issue #54).
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("JAVA_EXTENSIONS_WEBHOOK"))); v == "true" || v == "1" {
		img := os.Getenv("JAVA_EXTENSIONS_INIT_IMAGE")
		if img == "" {
			setupLog.Error(nil, "JAVA_EXTENSIONS_INIT_IMAGE is required with JAVA_EXTENSIONS_WEBHOOK=true")
			os.Exit(1)
		}
		mgr.GetWebhookServer().Register(dxwebhook.JavaExtensionsPath, &ctrlwebhook.Admission{Handler: &dxwebhook.JavaExtensionInjector{
			Client:  mgr.GetAPIReader(),
			Decoder: admission.NewDecoder(mgr.GetScheme()),
			Options: javaext.InjectOptions{InitImage: img, PullPolicy: corev1.PullPolicy(os.Getenv("JAVA_EXTENSIONS_INIT_PULL_POLICY")),
				ImagePullSecret: os.Getenv("JAVA_EXTENSIONS_INIT_PULL_SECRET")},
		}})
		setupLog.Info("java extensions webhook enabled", "path", dxwebhook.JavaExtensionsPath, "initImage", img)
	}
	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("Starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "Failed to run manager")
		os.Exit(1)
	}
}

// leaderElectionID: LEADER_ELECTION_ID (Helm value leaderElectionID) or the lease of every release so far (kept in
// 0.13.0 so an old and a new pod never run at the same time).
func leaderElectionID() string {
	if v := strings.TrimSpace(os.Getenv("LEADER_ELECTION_ID")); v != "" {
		return v
	}
	return "hcl-dx-scriptapp-operator.europesip.com"
}

// enabledControllers returns whether a controller runs (ENABLE_CONTROLLERS; empty: all).
func enabledControllers() func(string) bool {
	set := map[string]bool{}
	for _, n := range strings.Split(os.Getenv("ENABLE_CONTROLLERS"), ",") {
		if n = strings.TrimSpace(strings.ToLower(n)); n != "" {
			set[n] = true
		}
	}
	return func(name string) bool { return len(set) == 0 || set[name] }
}
