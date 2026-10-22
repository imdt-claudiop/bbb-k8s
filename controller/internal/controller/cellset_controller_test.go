package controller

import (
	"context"
	"fmt"
	appsv1 "k8s.io/api/apps/v1"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	bbbv1alpha1 "github.com/imdt-claudiop/bbb-k8s/controller/api/v1alpha1"
	"github.com/imdt-claudiop/bbb-k8s/controller/internal/balancer"
)

// fakeHelm keeps releases in memory and reports what ran.
type fakeHelm struct {
	version   string
	deployed  map[string]bool
	testFails map[string]bool
	applied   map[string]map[string]any
}

func newFakeHelm() *fakeHelm {
	return &fakeHelm{version: "0.4.0", deployed: map[string]bool{},
		testFails: map[string]bool{}, applied: map[string]map[string]any{}}
}
func (f *fakeHelm) ChartVersion() (string, error) { return f.version, nil }
func (f *fakeHelm) Deployed(ns string) (bool, string, error) {
	host, _ := f.applied[ns]["publicHost"].(string)
	return f.deployed[ns], host, nil
}
func (f *fakeHelm) Apply(ns string, v map[string]any) error {
	f.deployed[ns] = true
	f.applied[ns] = v
	return nil
}
func (f *fakeHelm) Test(ns string) error {
	if f.testFails[ns] {
		return fmt.Errorf("api-smoke failed")
	}
	return nil
}
func (f *fakeHelm) Uninstall(ns string) error { delete(f.deployed, ns); return nil }

// fakeBalancer records hooks; state per API URL.
type fakeBalancer struct {
	state map[string]string // enabled | cordoned
	load  map[string]float64
}

func newFakeBalancer() *fakeBalancer {
	return &fakeBalancer{state: map[string]string{}, load: map[string]float64{}}
}
func (f *fakeBalancer) Register(_ context.Context, api, secret string) error {
	f.state[api] = "enabled"
	return nil
}
func (f *fakeBalancer) Suspend(_ context.Context, api string) error {
	if _, ok := f.state[api]; ok {
		f.state[api] = "cordoned"
	}
	return nil
}
func (f *fakeBalancer) Deregister(_ context.Context, api string) error {
	delete(f.state, api)
	return nil
}
func (f *fakeBalancer) Lookup(_ context.Context, api string) (*balancer.Server, error) {
	st, ok := f.state[api]
	if !ok {
		return nil, nil
	}
	return &balancer.Server{ID: api, Host: api, State: st, Load: f.load[api]}, nil
}

type world struct {
	r        *CellSetReconciler
	helm     *fakeHelm
	bal      *fakeBalancer
	meetings map[string][2]int // host -> {meetings, participants}
	pending  map[string]int    // cellNS -> tars
	c        client.WithWatch
}

func newWorld(t *testing.T, objs ...client.Object) *world {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = bbbv1alpha1.AddToScheme(scheme)
	w := &world{helm: newFakeHelm(), bal: newFakeBalancer(),
		meetings: map[string][2]int{}, pending: map[string]int{}}
	w.c = fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&bbbv1alpha1.CellSet{}).WithObjects(objs...).Build()
	w.r = &CellSetReconciler{
		Client:     w.c,
		HelmClient: w.helm,
		MakeBalancer: func(context.Context, *CellSetReconciler, *bbbv1alpha1.CellSet) (balancer.Balancer, error) {
			return w.bal, nil
		},
		PendingRecordings: func(_ context.Context, ns string) (int, error) { return w.pending[ns], nil },
		Meetings: func(_ context.Context, host, _ string) (int, int, error) {
			m := w.meetings[host]
			return m[0], m[1], nil
		},
		LookupHost: func(_ context.Context, host string) ([]string, error) { return []string{"10.0.0.9"}, nil },
		CellLabel:  "bbb-k8s/cell",
		RoleLabel:  "bbb-k8s/role",
		VPCCIDR:    "10.116.0.0/20",
	}
	return w
}

func cellset(replicas int32) *bbbv1alpha1.CellSet {
	return &bbbv1alpha1.CellSet{
		ObjectMeta: metav1.ObjectMeta{Name: "bbb-41", Namespace: "cellsets", UID: "u1"},
		Spec: bbbv1alpha1.CellSetSpec{
			Replicas:         &replicas,
			ProvisionTimeout: metav1.Duration{Duration: 20 * time.Minute},
			Version:          bbbv1alpha1.VersionSpec{Chart: "0.4.0", ValuesRef: "bbb-41-values"},
			Host:             bbbv1alpha1.HostSpec{Template: "cell{{.index}}-ip-{{.ip}}.bbb.example", TLSSecretRef: "bbb-wildcard-tls"},
			Balancer:         bbbv1alpha1.BalancerSpec{Kind: "scalelite", URL: "https://sl.example/scalelite/api", SecretRef: "scalelite-api"},
		},
	}
}

// cellWorldObjects: everything a cell needs to provision in the fake cluster.
func cellWorldObjects(n int) []client.Object {
	name := fmt.Sprintf("cell-%d", n)
	objs := []client.Object{
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "bbb-41-values", Namespace: "cellsets"},
			Data: map[string]string{"values.yaml": "nodeRoleLabel: bbb-k8s/role\nrecording:\n  scalelite:\n    spool:\n      host: spool@sl\n"}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "bbb-wildcard-tls", Namespace: "cellsets"},
			Data: map[string][]byte{"tls.crt": []byte("c"), "tls.key": []byte("k")}},
	}
	for _, role := range []string{"storage", "media", "control", "data"} {
		node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
			Name:   fmt.Sprintf("%s-%s", name, role),
			Labels: map[string]string{"bbb-k8s/cell": fmt.Sprintf("%d", n), "bbb-k8s/role": role}}}
		if role == "media" {
			node.Status.Addresses = []corev1.NodeAddress{
				{Type: corev1.NodeExternalIP, Address: "203.0.113.5"},
				{Type: corev1.NodeInternalIP, Address: "10.116.0.5"}}
		}
		objs = append(objs, node)
	}
	return objs
}

func lbService(ns, ip string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "bbb-nginx", Namespace: ns},
		Status: corev1.ServiceStatus{LoadBalancer: corev1.LoadBalancerStatus{
			Ingress: []corev1.LoadBalancerIngress{{IP: ip}}}},
	}
}

func reconcile(t *testing.T, w *world) *bbbv1alpha1.CellSet {
	t.Helper()
	_, err := w.r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "bbb-41", Namespace: "cellsets"}})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	var cs bbbv1alpha1.CellSet
	if err := w.c.Get(context.Background(), types.NamespacedName{Name: "bbb-41", Namespace: "cellsets"}, &cs); err != nil {
		t.Fatalf("get: %v", err)
	}
	return &cs
}

func phaseOf(cs *bbbv1alpha1.CellSet, name string) bbbv1alpha1.CellPhase {
	for _, c := range cs.Status.Cells {
		if c.Name == name {
			return c.Phase
		}
	}
	return ""
}

func TestChartVersionMismatchRefused(t *testing.T) {
	cs := cellset(1)
	cs.Spec.Version.Chart = "9.9.9"
	w := newWorld(t, cs)
	got := reconcile(t, w)
	if len(got.Status.Cells) != 0 {
		t.Fatalf("acted on a version this controller does not carry: %+v", got.Status.Cells)
	}
	cond := meta.FindStatusCondition(got.Status.Conditions, "VersionSupported")
	if cond == nil || cond.Status != metav1.ConditionFalse {
		t.Fatalf("missing VersionSupported=False: %+v", got.Status.Conditions)
	}
}

func TestProvisionWaitsForNodePool(t *testing.T) {
	w := newWorld(t, cellset(1)) // no nodes at all
	got := reconcile(t, w)
	if got.Status.NextCell != 2 || len(got.Status.Cells) != 1 {
		t.Fatalf("planning: next=%d cells=%+v", got.Status.NextCell, got.Status.Cells)
	}
	if phaseOf(got, "cell-1") != bbbv1alpha1.CellProvisioning {
		t.Fatalf("phase: %+v", got.Status.Cells)
	}
	// Waits for the operator's node pool; helm untouched.
	got = reconcile(t, w)
	if len(w.helm.applied) != 0 {
		t.Fatalf("installed without nodes: %+v", w.helm.applied)
	}
	if got.Status.Cells[0].Waiting == "" {
		t.Fatal("not telling the operator what it waits for")
	}
}

func TestProvisionToRegistered(t *testing.T) {
	objs := append(cellWorldObjects(1), cellset(1))
	w := newWorld(t, objs...)
	// Round 1: plans and installs the provisional release (host needs the LB IP).
	got := reconcile(t, w)
	if !w.helm.deployed["cell-1"] {
		t.Fatalf("provisional release missing: %+v", got.Status.Cells)
	}
	// The LB appears; readiness is judged on workloads, which the fake helm does not
	// create: inject them ready.
	if err := w.c.Create(context.Background(), lbService("cell-1", "10.0.0.9")); err != nil {
		t.Fatal(err)
	}
	injectReadyWorkloads(t, w, "cell-1")
	got = reconcile(t, w) // renders final host, applies, workloads+dns+test pass -> Ready
	if h := got.Status.Cells[0].Host; h != "cell1-ip-10-0-0-9.bbb.example" {
		t.Fatalf("host: %q", h)
	}
	if phaseOf(got, "cell-1") != bbbv1alpha1.CellReady {
		t.Fatalf("not Ready: %+v", got.Status.Cells)
	}
	got = reconcile(t, w) // Ready -> Registered
	if phaseOf(got, "cell-1") != bbbv1alpha1.CellRegistered {
		t.Fatalf("not Registered: %+v", got.Status.Cells)
	}
	api := "https://cell1-ip-10-0-0-9.bbb.example/bigbluebutton/api"
	if w.bal.state[api] != "enabled" {
		t.Fatalf("balancer state: %+v", w.bal.state)
	}
	if got.Status.Replicas != 1 {
		t.Fatalf("status.replicas: %d", got.Status.Replicas)
	}
	// Per-cell secrets generated + TLS copied.
	var salt corev1.Secret
	if err := w.c.Get(context.Background(), types.NamespacedName{Namespace: "cell-1", Name: "bbb-salt"}, &salt); err != nil {
		t.Fatalf("bbb-salt: %v", err)
	}
	var tls corev1.Secret
	if err := w.c.Get(context.Background(), types.NamespacedName{Namespace: "cell-1", Name: "bbb-tls"}, &tls); err != nil {
		t.Fatalf("bbb-tls: %v", err)
	}
	// Per-cell values: cell number, host, media IP announced.
	vals := w.helm.applied["cell-1"]
	if vals["cell"] != "1" || vals["publicHost"] != "cell1-ip-10-0-0-9.bbb.example" {
		t.Fatalf("values: %+v", vals)
	}
}

func injectReadyWorkloads(t *testing.T, w *world, ns string) {
	t.Helper()
	one := int32(1)
	d := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "bbb-web", Namespace: ns},
		Spec: appsv1.DeploymentSpec{Replicas: &one,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"a": "b"}},
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"a": "b"}},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "x", Image: "x"}}}}},
		Status: appsv1.DeploymentStatus{ReadyReplicas: 1}}
	if err := w.c.Create(context.Background(), d); err != nil {
		t.Fatal(err)
	}
}

func registeredWorld(t *testing.T, replicas int32) (*world, *bbbv1alpha1.CellSet) {
	objs := append(cellWorldObjects(1), cellWorldObjects(2)[2:]...) // nodes of cell-2 too
	cs := cellset(replicas)
	cs.Status.NextCell = 3
	cs.Status.Cells = []bbbv1alpha1.CellStatus{
		{Name: "cell-1", Phase: bbbv1alpha1.CellRegistered, Host: "c1.example",
			Created: metav1.NewTime(time.Now().Add(-2 * time.Hour)), PhaseSince: metav1.Now()},
		{Name: "cell-2", Phase: bbbv1alpha1.CellRegistered, Host: "c2.example",
			Created: metav1.NewTime(time.Now().Add(-1 * time.Hour)), PhaseSince: metav1.Now()},
	}
	objs = append(objs, cs,
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "bbb-salt", Namespace: "cell-1"},
			StringData: map[string]string{"security-salt": "s1"}, Data: map[string][]byte{"security-salt": []byte("s1")}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "bbb-salt", Namespace: "cell-2"},
			StringData: map[string]string{"security-salt": "s2"}, Data: map[string][]byte{"security-salt": []byte("s2")}})
	w := newWorld(t, objs...)
	w.helm.deployed["cell-1"], w.helm.deployed["cell-2"] = true, true
	w.bal.state["https://c1.example/bigbluebutton/api"] = "enabled"
	w.bal.state["https://c2.example/bigbluebutton/api"] = "enabled"
	return w, cs
}

// backdateRetiring ages a Retiring cell past the drain grace.
func backdateRetiring(t *testing.T, w *world, name string) {
	t.Helper()
	var cs bbbv1alpha1.CellSet
	if err := w.c.Get(context.Background(), types.NamespacedName{Name: "bbb-41", Namespace: "cellsets"}, &cs); err != nil {
		t.Fatal(err)
	}
	for i := range cs.Status.Cells {
		if cs.Status.Cells[i].Name == name {
			cs.Status.Cells[i].PhaseSince = metav1.NewTime(time.Now().Add(-3 * time.Minute))
		}
	}
	if err := w.c.Status().Update(context.Background(), &cs); err != nil {
		t.Fatal(err)
	}
}

func TestScaleDownPicksFewestParticipantsTieOldest(t *testing.T) {
	w, _ := registeredWorld(t, 1)
	w.meetings["c1.example"] = [2]int{1, 5} // 5 participants
	w.meetings["c2.example"] = [2]int{1, 2} // fewest -> victim
	got := reconcile(t, w)
	if phaseOf(got, "cell-2") != bbbv1alpha1.CellRetiring || phaseOf(got, "cell-1") != bbbv1alpha1.CellRegistered {
		t.Fatalf("victim: %+v", got.Status.Cells)
	}
	if w.bal.state["https://c2.example/bigbluebutton/api"] != "cordoned" {
		t.Fatalf("not suspended: %+v", w.bal.state)
	}
	// Tie: the oldest goes. Quiet from the start, it still holds for the drain grace, then
	// completes: cell-1 (older) deregistered and uninstalled, cell-2 untouched.
	w2, _ := registeredWorld(t, 1)
	w2.meetings["c1.example"] = [2]int{0, 0}
	w2.meetings["c2.example"] = [2]int{0, 0}
	got2 := reconcile(t, w2)
	if phaseOf(got2, "cell-1") != bbbv1alpha1.CellRetiring || !w2.helm.deployed["cell-1"] {
		t.Fatalf("tie victim should be Retiring inside the grace: %+v", got2.Status.Cells)
	}
	backdateRetiring(t, w2, "cell-1")
	got2 = reconcile(t, w2)
	if w2.helm.deployed["cell-1"] {
		t.Fatalf("tie should retire the oldest (cell-1): %+v", got2.Status.Cells)
	}
	if _, ok := w2.bal.state["https://c1.example/bigbluebutton/api"]; ok {
		t.Fatalf("cell-1 still registered: %+v", w2.bal.state)
	}
	if phaseOf(got2, "cell-2") != bbbv1alpha1.CellRegistered || !w2.helm.deployed["cell-2"] {
		t.Fatalf("cell-2 touched: %+v", got2.Status.Cells)
	}
}

func TestRetiringWaitsThenRetires(t *testing.T) {
	w, _ := registeredWorld(t, 1)
	w.meetings["c1.example"] = [2]int{1, 3} // fewest participants, still meeting -> waits
	w.meetings["c2.example"] = [2]int{2, 7}
	got := reconcile(t, w) // cell-1 suspended, then waits on its meeting
	if phaseOf(got, "cell-1") != bbbv1alpha1.CellRetiring {
		t.Fatalf("victim: %+v", got.Status.Cells)
	}
	if w.bal.state["https://c1.example/bigbluebutton/api"] != "cordoned" {
		t.Fatalf("not suspended: %+v", w.bal.state)
	}
	backdateRetiring(t, w, "cell-1")
	got = reconcile(t, w)
	if phaseOf(got, "cell-1") != bbbv1alpha1.CellRetiring || !w.helm.deployed["cell-1"] {
		t.Fatalf("retired with meetings: %+v", got.Status.Cells)
	}
	// Meetings over, a recording still pending: waits.
	w.meetings["c1.example"] = [2]int{0, 0}
	w.pending["cell-1"] = 1
	got = reconcile(t, w)
	if !w.helm.deployed["cell-1"] {
		t.Fatal("uninstalled with recordings pending")
	}
	// Delivered, balancer at zero load: deregister + uninstall; Retired once volumes and
	// namespace are gone (the fake cluster has none).
	w.pending["cell-1"] = 0
	got = reconcile(t, w)
	if w.helm.deployed["cell-1"] {
		t.Fatal("release still deployed")
	}
	if _, ok := w.bal.state["https://c1.example/bigbluebutton/api"]; ok {
		t.Fatalf("still registered: %+v", w.bal.state)
	}
	got = reconcile(t, w)
	if phaseOf(got, "cell-1") != "" { // pruned after Retired
		t.Fatalf("not pruned: %+v", got.Status.Cells)
	}
	if got.Status.Replicas != 1 {
		t.Fatalf("replicas: %d", got.Status.Replicas)
	}
	// The number is never reused.
	if got.Status.NextCell != 3 {
		t.Fatalf("counter went back: %d", got.Status.NextCell)
	}
}

func TestRefusesZeroingOnlyRegisteredSet(t *testing.T) {
	w, _ := registeredWorld(t, 0)
	w.meetings["c1.example"] = [2]int{0, 0}
	w.meetings["c2.example"] = [2]int{0, 0}
	got := reconcile(t, w)
	// One cell retires (2 -> 1); the last one is refused.
	if phaseOf(got, "cell-1") != bbbv1alpha1.CellRetiring {
		t.Fatalf("expected cell-1 retiring: %+v", got.Status.Cells)
	}
	backdateRetiring(t, w, "cell-1")
	got = reconcile(t, w)
	if w.helm.deployed["cell-1"] {
		t.Fatalf("expected cell-1 retired: %+v", got.Status.Cells)
	}
	cond := meta.FindStatusCondition(got.Status.Conditions, "ScaleToZeroRefused")
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("missing refusal condition: %+v", got.Status.Conditions)
	}
	if phaseOf(got, "cell-2") != bbbv1alpha1.CellRegistered {
		t.Fatalf("zeroed the only registered set: %+v", got.Status.Cells)
	}
}

func TestProvisionTimeoutFails(t *testing.T) {
	cs := cellset(1)
	cs.Status.NextCell = 2
	cs.Status.Cells = []bbbv1alpha1.CellStatus{{Name: "cell-1", Phase: bbbv1alpha1.CellProvisioning,
		PhaseSince: metav1.NewTime(time.Now().Add(-21 * time.Minute)), Created: metav1.NewTime(time.Now().Add(-21 * time.Minute))}}
	w := newWorld(t, cs)
	got := reconcile(t, w)
	if phaseOf(got, "cell-1") != bbbv1alpha1.CellFailed {
		t.Fatalf("not Failed: %+v", got.Status.Cells)
	}
	// Failed is not replaced on its own and keeps its number; the NEXT reconcile plans a
	// new cell for the gap under a fresh number.
	got = reconcile(t, w)
	if phaseOf(got, "cell-1") != bbbv1alpha1.CellFailed {
		t.Fatalf("Failed cell dropped: %+v", got.Status.Cells)
	}
	if phaseOf(got, "cell-2") != bbbv1alpha1.CellProvisioning || got.Status.Replicas != 1 {
		t.Fatalf("replacement: %+v replicas=%d", got.Status.Cells, got.Status.Replicas)
	}
	if got.Status.NextCell != 3 {
		t.Fatalf("next: %d", got.Status.NextCell)
	}
}

func TestProvisionTimeoutDefaultsTo20m(t *testing.T) {
	cs := cellset(1)
	cs.Spec.ProvisionTimeout = metav1.Duration{}
	if provisionTimeout(cs) != 20*time.Minute {
		t.Fatalf("fallback: %v", provisionTimeout(cs))
	}
}

func TestNextCellSeedAnnotation(t *testing.T) {
	cs := cellset(1)
	cs.Annotations = map[string]string{NextCellAnnotation: "3"}
	w := newWorld(t, cs)
	got := reconcile(t, w)
	if len(got.Status.Cells) != 1 || got.Status.Cells[0].Name != "cell-3" || got.Status.NextCell != 4 {
		t.Fatalf("seed ignored: %+v next=%d", got.Status.Cells, got.Status.NextCell)
	}
}

func TestRetiringHoldsForDrainGrace(t *testing.T) {
	w, _ := registeredWorld(t, 1)
	w.meetings["c1.example"] = [2]int{0, 0}
	w.meetings["c2.example"] = [2]int{0, 0}
	got := reconcile(t, w) // suspends the victim; the grace keeps it deployed
	if !w.helm.deployed["cell-1"] {
		t.Fatalf("torn down inside the drain grace: %+v", got.Status.Cells)
	}
	if phaseOf(got, "cell-1") != bbbv1alpha1.CellRetiring {
		t.Fatalf("victim: %+v", got.Status.Cells)
	}
	// Grace over (backdate), quiet: the retirement completes.
	for i := range got.Status.Cells {
		if got.Status.Cells[i].Name == "cell-1" {
			got.Status.Cells[i].PhaseSince = metav1.NewTime(time.Now().Add(-3 * time.Minute))
		}
	}
	if err := w.c.Status().Update(context.Background(), got); err != nil {
		t.Fatal(err)
	}
	reconcile(t, w)
	if w.helm.deployed["cell-1"] {
		t.Fatal("still deployed after the grace")
	}
}
