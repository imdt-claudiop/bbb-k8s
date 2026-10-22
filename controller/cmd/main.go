// The bbb-k8s cell controller (phase 2 spec, "A CRD e o controller"): reconciles CellSets,
// one cell per namespace, behind the balancer named by the spec. Runs in its own namespace
// in-cluster, or locally against a kubeconfig (the PoC proof path).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	bbbv1alpha1 "github.com/imdt-claudiop/bbb-k8s/controller/api/v1alpha1"
	"github.com/imdt-claudiop/bbb-k8s/controller/internal/balancer"
	"github.com/imdt-claudiop/bbb-k8s/controller/internal/cells"
	"github.com/imdt-claudiop/bbb-k8s/controller/internal/controller"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(bbbv1alpha1.AddToScheme(scheme))
}

func main() {
	var chartPath, vpcCIDR, cellLabel, roleLabel, probeAddr string
	flag.StringVar(&chartPath, "chart", "/chart", "path of the cell chart this controller carries")
	flag.StringVar(&vpcCIDR, "vpc-cidr", "", "VPC range announced to LiveKit besides the media node IP")
	flag.StringVar(&cellLabel, "cell-label", "bbb-k8s/cell", "node label naming the cell")
	flag.StringVar(&roleLabel, "role-label", "bbb-k8s/role", "node label naming the role")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "health probe address")
	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: probeAddr,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "manager:", err)
		os.Exit(1)
	}

	clientset, err := kubernetes.NewForConfig(mgr.GetConfig())
	if err != nil {
		fmt.Fprintln(os.Stderr, "clientset:", err)
		os.Exit(1)
	}
	rec := &controller.CellSetReconciler{
		Client:     mgr.GetClient(),
		RestConfig: mgr.GetConfig(),
		Clientset:  clientset,
		HelmClient: cells.SDK{Helm: cells.NewHelm(chartPath)},
		MakeBalancer: func(ctx context.Context, r *controller.CellSetReconciler, cs *bbbv1alpha1.CellSet) (balancer.Balancer, error) {
			var s corev1.Secret
			if err := r.Get(ctx, types.NamespacedName{Namespace: cs.Namespace, Name: cs.Spec.Balancer.SecretRef}, &s); err != nil {
				return nil, err
			}
			secret := string(s.Data["loadbalancer-secret"])
			if secret == "" {
				return nil, fmt.Errorf("secret %s/%s has no loadbalancer-secret key", cs.Namespace, cs.Spec.Balancer.SecretRef)
			}
			return balancer.NewScalelite(cs.Spec.Balancer.URL, secret), nil
		},
		LookupHost: controller.DefaultLookupHost,
		Meetings:   controller.DefaultMeetings,
		CellLabel:  cellLabel,
		RoleLabel:  roleLabel,
		VPCCIDR:    vpcCIDR,
	}
	rec.PendingRecordings = func(ctx context.Context, cellNS string) (int, error) {
		return cells.PendingRecordings(ctx, mgr.GetConfig(), clientset, cellNS)
	}
	if err := rec.SetupWithManager(mgr); err != nil {
		fmt.Fprintln(os.Stderr, "controller:", err)
		os.Exit(1)
	}
	_ = mgr.AddHealthzCheck("healthz", healthz.Ping)
	_ = mgr.AddReadyzCheck("readyz", healthz.Ping)
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		fmt.Fprintln(os.Stderr, "start:", err)
		os.Exit(1)
	}
}
