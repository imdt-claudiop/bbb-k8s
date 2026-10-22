// Package controller reconciles CellSets: the lifecycle loop of phase 2 ("A CRD e o
// controller"). It routes each cell through Provisioning -> Ready -> Registered and, on the
// way out, Registered -> Retiring -> Retired, with Failed for a provision that misses its
// deadline. Every transition is idempotent and resumable: the only memory is the CellSet
// status plus what the cluster itself holds.
package controller

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	bbbv1alpha1 "github.com/imdt-claudiop/bbb-k8s/controller/api/v1alpha1"
	"github.com/imdt-claudiop/bbb-k8s/controller/internal/balancer"
	"github.com/imdt-claudiop/bbb-k8s/controller/internal/cells"
)

const (
	cellSetLabel = "bbb-k8s/cellset"
	requeue      = 15 * time.Second
)

// Helm is what the reconciler needs from the Helm side (the real SDK in production, a fake
// in tests).
type Helm interface {
	ChartVersion() (string, error)
	// Deployed reports whether the release exists and the publicHost it carries.
	Deployed(namespace string) (bool, string, error)
	Apply(namespace string, values map[string]any) error
	Test(namespace string) error
	Uninstall(namespace string) error
}

// NewBalancer builds the balancer for a spec; swapped in tests.
type NewBalancer func(ctx context.Context, r *CellSetReconciler, cs *bbbv1alpha1.CellSet) (balancer.Balancer, error)

// PendingRecordings counts undelivered recordings of a cell; swapped in tests.
type PendingRecordings func(ctx context.Context, cellNS string) (int, error)

// MeetingsFn reads meetings/participants through the cell's own BBB API; swapped in tests.
type MeetingsFn func(ctx context.Context, host, salt string) (meetings, participants int, err error)

// CellSetReconciler reconciles a CellSet object.
type CellSetReconciler struct {
	client.Client
	RestConfig *rest.Config
	Clientset  kubernetes.Interface

	HelmClient        Helm
	MakeBalancer      NewBalancer
	PendingRecordings PendingRecordings
	Meetings          MeetingsFn
	LookupHost        func(ctx context.Context, host string) ([]string, error)

	CellLabel string // bbb-k8s/cell
	RoleLabel string // bbb-k8s/role
	VPCCIDR   string // announced to LiveKit besides the media node IP
}

// +kubebuilder:rbac:groups=bbb-k8s.imdt.dev,resources=cellsets,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=bbb-k8s.imdt.dev,resources=cellsets/status;cellsets/scale,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=namespaces;secrets;configmaps;services;serviceaccounts;persistentvolumeclaims;pods;nodes;events,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods/exec,verbs=create
// +kubebuilder:rbac:groups=apps,resources=deployments;statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=batch,resources=jobs;cronjobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cilium.io,resources=ciliumnetworkpolicies,verbs=get;list;watch;create;update;patch;delete

func (r *CellSetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	var cs bbbv1alpha1.CellSet
	if err := r.Get(ctx, req.NamespacedName, &cs); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	orig := cs.DeepCopy()

	// The controller carries the chart of the commit it was built from and refuses another
	// version (publishing charts to a registry is later work).
	chartVersion, err := r.HelmClient.ChartVersion()
	if err != nil {
		return ctrl.Result{}, err
	}
	if cs.Spec.Version.Chart != chartVersion {
		r.setCondition(&cs, "VersionSupported", metav1.ConditionFalse, "ChartMismatch",
			fmt.Sprintf("this controller carries chart %s, the spec asks %s", chartVersion, cs.Spec.Version.Chart))
		return r.finish(ctx, orig, &cs, ctrl.Result{})
	}
	r.setCondition(&cs, "VersionSupported", metav1.ConditionTrue, "ChartMatches", "")

	cs.Status.Selector = cellSetLabel + "=" + cs.Name
	if err := r.ensureNextCell(ctx, &cs); err != nil {
		return ctrl.Result{}, err
	}

	desired := int32(0)
	if cs.Spec.Replicas != nil {
		desired = *cs.Spec.Replicas
	}

	// The refusal of the spec: never scale to zero the only CellSet with Registered cells
	// on a balancer (for now controller-side with a condition; an admission webhook can
	// make the kubectl itself fail, phase 4).
	if desired == 0 && r.hasPhase(&cs, bbbv1alpha1.CellRegistered) {
		only, err := r.onlyRegisteredSetOnBalancer(ctx, &cs)
		if err != nil {
			return ctrl.Result{}, err
		}
		if only {
			desired = 1
			r.setCondition(&cs, "ScaleToZeroRefused", metav1.ConditionTrue, "OnlyRegisteredCellSet",
				"this is the only CellSet with Registered cells on this balancer; keeping one cell")
		}
	} else {
		meta.RemoveStatusCondition(&cs.Status.Conditions, "ScaleToZeroRefused")
	}

	// Scale up: new cells for the gap, numbered by the global counter. The node pool comes
	// first (the operator's step), so a cell may sit waiting for its nodes. A Failed cell
	// keeps occupying its slot until the operator deletes its status entry (the spec rules
	// out the controller replacing it on its own), so it counts against the gap.
	for r.serving(&cs)+r.failed(&cs) < desired {
		n := cs.Status.NextCell
		cs.Status.NextCell = n + 1
		cs.Status.Cells = append(cs.Status.Cells, bbbv1alpha1.CellStatus{
			Name: fmt.Sprintf("cell-%d", n), Phase: bbbv1alpha1.CellProvisioning,
			PhaseSince: metav1.Now(), Created: metav1.Now(),
		})
		logger.Info("cell planned", "cell", fmt.Sprintf("cell-%d", n))
	}

	// Scale down: one retirement at a time (spec, "Escala": retiring is always one cell at
	// a time). Prefer cells not yet serving traffic.
	if r.serving(&cs) > desired && !r.hasPhase(&cs, bbbv1alpha1.CellRetiring) {
		if err := r.startRetirement(ctx, &cs); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Progress every cell one step.
	again := false
	for i := range cs.Status.Cells {
		cell := &cs.Status.Cells[i]
		var err error
		var wait bool
		switch cell.Phase {
		case bbbv1alpha1.CellProvisioning:
			wait, err = r.provision(ctx, &cs, cell)
		case bbbv1alpha1.CellReady:
			wait, err = r.register(ctx, &cs, cell)
		case bbbv1alpha1.CellRetiring:
			wait, err = r.retire(ctx, &cs, cell)
		}
		if err != nil {
			logger.Error(err, "cell step", "cell", cell.Name, "phase", cell.Phase)
			cell.Waiting = "error: " + err.Error()
			again = true
			continue
		}
		again = again || wait
	}

	// A Failed cell holds its replica slot: say so, and say what releases it.
	if names := r.failedNames(&cs); len(names) > 0 {
		r.setCondition(&cs, "ReplicasHeldByFailedCell", metav1.ConditionTrue, "FailedCellHoldsSlot",
			fmt.Sprintf("failed cells %s hold their replica slots; delete their entries from status.cells to release them (the controller never replaces a Failed cell on its own)", strings.Join(names, ", ")))
	} else {
		meta.RemoveStatusCondition(&cs.Status.Conditions, "ReplicasHeldByFailedCell")
	}

	// Retired cells leave the list: their namespaces are gone and the number is never
	// reused (NextCell only grows).
	kept := cs.Status.Cells[:0]
	for _, c := range cs.Status.Cells {
		if c.Phase != bbbv1alpha1.CellRetired {
			kept = append(kept, c)
		}
	}
	cs.Status.Cells = kept
	cs.Status.Replicas = r.serving(&cs)

	res := ctrl.Result{}
	if again {
		res.RequeueAfter = requeue
	}
	return r.finish(ctx, orig, &cs, res)
}

func (r *CellSetReconciler) finish(ctx context.Context, orig, cs *bbbv1alpha1.CellSet, res ctrl.Result) (ctrl.Result, error) {
	if err := r.Status().Patch(ctx, cs, client.MergeFrom(orig)); err != nil {
		return ctrl.Result{}, err
	}
	return res, nil
}

func (r *CellSetReconciler) setCondition(cs *bbbv1alpha1.CellSet, t string, s metav1.ConditionStatus, reason, msg string) {
	meta.SetStatusCondition(&cs.Status.Conditions, metav1.Condition{
		Type: t, Status: s, Reason: reason, Message: msg, ObservedGeneration: cs.Generation})
}

// serving counts Provisioning, Ready and Registered (the spec's status.replicas).
func (r *CellSetReconciler) serving(cs *bbbv1alpha1.CellSet) int32 {
	n := int32(0)
	for _, c := range cs.Status.Cells {
		switch c.Phase {
		case bbbv1alpha1.CellProvisioning, bbbv1alpha1.CellReady, bbbv1alpha1.CellRegistered:
			n++
		}
	}
	return n
}

// failed counts Failed cells; failedNames lists them.
func (r *CellSetReconciler) failed(cs *bbbv1alpha1.CellSet) int32 {
	return int32(len(r.failedNames(cs)))
}

func (r *CellSetReconciler) failedNames(cs *bbbv1alpha1.CellSet) []string {
	var names []string
	for _, c := range cs.Status.Cells {
		if c.Phase == bbbv1alpha1.CellFailed {
			names = append(names, c.Name)
		}
	}
	return names
}

func (r *CellSetReconciler) hasPhase(cs *bbbv1alpha1.CellSet, p bbbv1alpha1.CellPhase) bool {
	for _, c := range cs.Status.Cells {
		if c.Phase == p {
			return true
		}
	}
	return false
}

// NextCellAnnotation seeds the global counter when adopting a cluster whose cell history
// predates the controller (numbers of deleted cells are never reused; the cluster alone
// cannot remember them).
const NextCellAnnotation = "bbb-k8s.imdt.dev/next-cell"

// ensureNextCell keeps the global, ever-increasing counter (decision A): the max over the
// operator's seed annotation, every CellSet's own high-water mark and every cell-<n>
// namespace that exists, plus one. It never goes down, so a number is never reused even
// after its namespace is gone.
func (r *CellSetReconciler) ensureNextCell(ctx context.Context, cs *bbbv1alpha1.CellSet) error {
	next := cs.Status.NextCell
	if next < 1 {
		next = 1
	}
	if seed, err := strconv.Atoi(cs.Annotations[NextCellAnnotation]); err == nil && int32(seed) > next {
		next = int32(seed)
	}
	var sets bbbv1alpha1.CellSetList
	if err := r.List(ctx, &sets); err != nil {
		return err
	}
	for _, other := range sets.Items {
		if other.Status.NextCell > next {
			next = other.Status.NextCell
		}
	}
	var nss corev1.NamespaceList
	if err := r.List(ctx, &nss); err != nil {
		return err
	}
	for _, ns := range nss.Items {
		if n, ok := cellNumber(ns.Name); ok && n >= next {
			next = n + 1
		}
	}
	cs.Status.NextCell = next
	return nil
}

func cellNumber(ns string) (int32, bool) {
	if !strings.HasPrefix(ns, "cell-") {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(ns, "cell-"))
	if err != nil || n < 0 {
		return 0, false
	}
	return int32(n), true
}

// onlyRegisteredSetOnBalancer says whether no other CellSet with the same balancer URL has
// Registered cells.
func (r *CellSetReconciler) onlyRegisteredSetOnBalancer(ctx context.Context, cs *bbbv1alpha1.CellSet) (bool, error) {
	var sets bbbv1alpha1.CellSetList
	if err := r.List(ctx, &sets); err != nil {
		return false, err
	}
	for _, other := range sets.Items {
		if other.UID == cs.UID || other.Spec.Balancer.URL != cs.Spec.Balancer.URL {
			continue
		}
		for _, c := range other.Status.Cells {
			if c.Phase == bbbv1alpha1.CellRegistered {
				return false, nil
			}
		}
	}
	return true, nil
}

// startRetirement picks the Registered cell with the fewest participants (tie: the oldest)
// and suspends it on the balancer: the point of no return.
func (r *CellSetReconciler) startRetirement(ctx context.Context, cs *bbbv1alpha1.CellSet) error {
	logger := log.FromContext(ctx)
	type candidate struct {
		idx          int
		participants int
		created      time.Time
	}
	var cands []candidate
	for i := range cs.Status.Cells {
		c := &cs.Status.Cells[i]
		if c.Phase != bbbv1alpha1.CellRegistered {
			continue
		}
		salt, err := cells.Salt(ctx, r.Client, c.Name)
		if err != nil {
			return err
		}
		_, participants, err := r.Meetings(ctx, c.Host, salt)
		if err != nil {
			return fmt.Errorf("participants of %s: %w", c.Name, err)
		}
		cands = append(cands, candidate{i, participants, c.Created.Time})
	}
	if len(cands) == 0 {
		// Nothing Registered to retire yet; a Provisioning/Ready surplus cell is retired
		// directly (it never served, nothing to drain).
		for i := range cs.Status.Cells {
			c := &cs.Status.Cells[i]
			if c.Phase == bbbv1alpha1.CellProvisioning || c.Phase == bbbv1alpha1.CellReady {
				c.Phase = bbbv1alpha1.CellRetiring
				c.PhaseSince = metav1.Now()
				return nil
			}
		}
		return nil
	}
	sort.Slice(cands, func(a, b int) bool {
		if cands[a].participants != cands[b].participants {
			return cands[a].participants < cands[b].participants
		}
		return cands[a].created.Before(cands[b].created)
	})
	victim := &cs.Status.Cells[cands[0].idx]
	b, err := r.MakeBalancer(ctx, r, cs)
	if err != nil {
		return err
	}
	salt, err := cells.Salt(ctx, r.Client, victim.Name)
	if err != nil {
		return err
	}
	if err := b.Suspend(ctx, cells.NewBBBClient(victim.Host, salt, false).APIURL()); err != nil {
		return err
	}
	logger.Info("cell suspended", "cell", victim.Name, "participants", cands[0].participants)
	victim.Phase = bbbv1alpha1.CellRetiring
	victim.PhaseSince = metav1.Now()
	return nil
}
