package controller

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	autoscalingv1 "k8s.io/api/autoscaling/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	bbbv1alpha1 "github.com/imdt-claudiop/bbb-k8s/controller/api/v1alpha1"
)

// TestCRDAndScaleSubresource proves the generated CRD against a real kube-apiserver
// (envtest): the scale subresource is what makes `kubectl scale cellset` work and what an
// autoscaler talks to (phase 2 spec, "A CRD e o controller").
func TestCRDAndScaleSubresource(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS not set (setup-envtest); the fake-client tests cover the state machine")
	}
	env := &envtest.Environment{
		CRDDirectoryPaths: []string{filepath.Join("..", "..", "config", "crd")},
	}
	cfg, err := env.Start()
	if err != nil {
		t.Fatalf("envtest: %v", err)
	}
	defer func() { _ = env.Stop() }()

	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = bbbv1alpha1.AddToScheme(scheme)
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	one := int32(1)
	cs := &bbbv1alpha1.CellSet{
		ObjectMeta: metav1.ObjectMeta{Name: "bbb-41", Namespace: "default"},
		Spec: bbbv1alpha1.CellSetSpec{
			Replicas: &one,
			Version:  bbbv1alpha1.VersionSpec{Chart: "0.4.0", ValuesRef: "v"},
			Host:     bbbv1alpha1.HostSpec{Template: "cell-{{.index}}.x", TLSSecretRef: "tls"},
			Balancer: bbbv1alpha1.BalancerSpec{Kind: "scalelite", URL: "https://sl/api", SecretRef: "s"},
		},
	}
	if err := c.Create(ctx, cs); err != nil {
		t.Fatalf("create: %v", err)
	}
	// The CRD default of provisionTimeout applies to an ABSENT field (a typed client always
	// sends the Duration, so the code has its own fallback, tested elsewhere).
	un := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "bbb-k8s.imdt.dev/v1alpha1", "kind": "CellSet",
		"metadata": map[string]any{"name": "bbb-42", "namespace": "default"},
		"spec": map[string]any{
			"replicas": int64(1),
			"version":  map[string]any{"chart": "0.4.0", "valuesRef": "v"},
			"host":     map[string]any{"template": "cell-{{.index}}.x", "tlsSecretRef": "tls"},
			"balancer": map[string]any{"kind": "scalelite", "url": "https://sl/api", "secretRef": "s"},
		},
	}}
	if err := c.Create(ctx, un); err != nil {
		t.Fatalf("create unstructured: %v", err)
	}
	var defaulted bbbv1alpha1.CellSet
	if err := c.Get(ctx, types.NamespacedName{Name: "bbb-42", Namespace: "default"}, &defaulted); err != nil {
		t.Fatal(err)
	}
	if defaulted.Spec.ProvisionTimeout.Duration.Minutes() != 20 {
		t.Fatalf("provisionTimeout default: %v", defaulted.Spec.ProvisionTimeout)
	}
	var got bbbv1alpha1.CellSet
	if err := c.Get(ctx, types.NamespacedName{Name: "bbb-41", Namespace: "default"}, &got); err != nil {
		t.Fatal(err)
	}
	// Status subresource.
	got.Status.Replicas = 1
	got.Status.Selector = "bbb-k8s/cellset=bbb-41"
	if err := c.Status().Update(ctx, &got); err != nil {
		t.Fatalf("status: %v", err)
	}
	// The scale subresource, as kubectl scale and the HPA use it.
	var scale autoscalingv1.Scale
	if err := c.SubResource("scale").Get(ctx, &got, &scale); err != nil {
		t.Fatalf("get scale: %v", err)
	}
	if scale.Spec.Replicas != 1 || scale.Status.Replicas != 1 || scale.Status.Selector != "bbb-k8s/cellset=bbb-41" {
		t.Fatalf("scale: %+v", scale)
	}
	scale.Spec.Replicas = 2
	if err := c.SubResource("scale").Update(ctx, &got, client.WithSubResourceBody(&scale)); err != nil {
		t.Fatalf("update scale: %v", err)
	}
	if err := c.Get(ctx, types.NamespacedName{Name: "bbb-41", Namespace: "default"}, &got); err != nil {
		t.Fatal(err)
	}
	if *got.Spec.Replicas != 2 {
		t.Fatalf("spec.replicas after scale: %d", *got.Spec.Replicas)
	}
}
