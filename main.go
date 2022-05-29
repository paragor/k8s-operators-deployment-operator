/*
Copyright 2022 paragor.

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
	"fmt"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	batchv1beta1 "k8s.io/api/batch/v1beta1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	vpav1 "k8s.io/autoscaler/vertical-pod-autoscaler/pkg/apis/autoscaling.k8s.io/v1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"os"
	"sigs.k8s.io/controller-runtime/pkg/client"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/paragor/k8s-operators-deployment-operator/controllers"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var namespaceSelector string
	var probeAddr string
	var updateMode string

	registeredObjects := []struct {
		enabled     bool
		flagName    string
		flagComment string
		flagDefault bool
		objType     client.Object
	}{
		{
			flagName:    "enable-batchv1-cronjob",
			flagComment: "enable auto create vpa for batch/v1-cronjob (for new version cluster)",
			flagDefault: true,
			objType:     &batchv1.CronJob{},
		},
		{
			flagName:    "enable-batchv1beta1-cronjob",
			flagComment: "enable auto create vpa for batch/v1beta1-cronjob (for old version cluster)",
			flagDefault: false,
			objType:     &batchv1beta1.CronJob{},
		},
		{
			flagName:    "enable-deployments",
			flagComment: "enable auto create vpa for Deployment",
			flagDefault: true,
			objType:     &appsv1.Deployment{},
		},
		{
			flagName:    "enable-daemonsets",
			flagComment: "enable auto create vpa for DaemonSet",
			flagDefault: true,
			objType:     &appsv1.DaemonSet{},
		},
		{
			flagName:    "enable-statfulsets",
			flagComment: "enable auto create vpa for StatefulSet",
			flagDefault: true,
			objType:     &appsv1.StatefulSet{},
		},
	}
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.StringVar(&namespaceSelector, "namespace-selector", "", "selector for namespaces to serve, example: vpa=enabled")
	flag.StringVar(&updateMode, "update-mode", string(controllers.UpdateModeInitial), fmt.Sprintf("update mode for vpa, valid: %v", controllers.AvailableUpdateModes))
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	for i := range registeredObjects {
		flag.BoolVar(
			&registeredObjects[i].enabled,
			registeredObjects[i].flagName,
			registeredObjects[i].flagDefault,
			registeredObjects[i].flagComment,
		)
	}
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	{
		utilruntime.Must(clientgoscheme.AddToScheme(scheme))
		utilruntime.Must(vpav1.AddToScheme(scheme))

		utilruntime.Must(batchv1beta1.AddToScheme(scheme))
		utilruntime.Must(batchv1.AddToScheme(scheme))
		utilruntime.Must(appsv1.AddToScheme(scheme))

		//+kubebuilder:scaffold:scheme
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     metricsAddr,
		Port:                   9443,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "fd80f860.paragor.ru",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	atLeastOneRegistered := false
	for _, regObj := range registeredObjects {
		if !regObj.enabled {
			continue
		}
		atLeastOneRegistered = true
		copyObjType := regObj.objType
		reconciler, err := controllers.NewGeneralReconciler(
			mgr.GetClient(),
			mgr.GetScheme(),
			namespaceSelector,
			func() client.Object {
				return copyObjType
			},
			controllers.UpdateMode(updateMode),
		)
		if err != nil {
			setupLog.Error(err, "unable to create reconciler", "controller", fmt.Sprintf(
				"%T",
				copyObjType,
			))
			os.Exit(1)
		}
		if err = (reconciler).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", fmt.Sprintf(
				"%T",
				copyObjType,
			))
			os.Exit(1)
		}
	}
	if !atLeastOneRegistered {
		setupLog.Error(err, "no one resources is registered")
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

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
