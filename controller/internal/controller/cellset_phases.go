package controller

import (
	"context"
	"fmt"
	"net"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	bbbv1alpha1 "github.com/imdt-claudiop/bbb-k8s/controller/api/v1alpha1"
	"github.com/imdt-claudiop/bbb-k8s/controller/internal/cells"
)

// provisionTimeout: the spec value, with the spec's default when the field came in as its
// zero value (a Duration always serializes, so the CRD default only covers an absent field).
func provisionTimeout(cs *bbbv1alpha1.CellSet) time.Duration {
	if cs.Spec.ProvisionTimeout.Duration > 0 {
		return cs.Spec.ProvisionTimeout.Duration
	}
	return 20 * time.Minute
}

// provision drives one cell from nothing to Ready: namespace and secrets, the chart (with a
// provisional host first when the template needs the LoadBalancer IP), DNS, helm test. Every
// step re-runs safely; what is done stays done. Returns wait=true while something is pending.
func (r *CellSetReconciler) provision(ctx context.Context, cs *bbbv1alpha1.CellSet, cell *bbbv1alpha1.CellStatus) (bool, error) {
	logger := log.FromContext(ctx)
	n, _ := cellNumber(cell.Name)

	if time.Since(cell.PhaseSince.Time) > provisionTimeout(cs) {
		cell.Phase = bbbv1alpha1.CellFailed
		cell.Reason = fmt.Sprintf("not Ready within %s: %s", provisionTimeout(cs), cell.Waiting)
		cell.Waiting = ""
		logger.Info("cell failed", "cell", cell.Name, "reason", cell.Reason)
		return false, nil
	}

	// 0. The cell's nodes: the operator's prerequisite (labels + taint on the node pool).
	ready, missing, err := cells.CellNodesReady(ctx, r.Client, r.CellLabel, r.RoleLabel, fmt.Sprintf("%d", n))
	if err != nil {
		return false, err
	}
	if !ready {
		cell.Waiting = fmt.Sprintf("node pool: no node labelled %s=%d with roles %v", r.CellLabel, n, missing)
		return true, nil
	}

	// 1. Namespace (labelled as ours, for adoption on resume) and Secrets.
	if err := cells.EnsureNamespace(ctx, r.Client, cell.Name); err != nil {
		return false, err
	}
	var ns corev1.Namespace
	if err := r.Get(ctx, types.NamespacedName{Name: cell.Name}, &ns); err != nil {
		return false, err
	}
	if ns.Labels[cellSetLabel] != cs.Name {
		if ns.Labels == nil {
			ns.Labels = map[string]string{}
		}
		ns.Labels[cellSetLabel] = cs.Name
		if err := r.Update(ctx, &ns); err != nil {
			return false, err
		}
	}
	recordingName, err := r.recordingSecretName(ctx, cs)
	if err != nil {
		return false, err
	}
	if err := cells.EnsureSecrets(ctx, r.Client, cell.Name, cs.Namespace,
		cs.Spec.Host.TLSSecretRef, cs.Spec.RecordingSecretRef, recordingName); err != nil {
		return false, err
	}

	// 2. The chart. With {{.ip}} in the template the first install carries a provisional
	// host; the final host comes from the LoadBalancer IP, as in phase 1.
	frozen, err := cells.FrozenValues(ctx, r.Client, cs.Namespace, cs.Spec.Version.ValuesRef)
	if err != nil {
		return false, err
	}
	mediaIP, _, err := cells.MediaNode(ctx, r.Client, r.CellLabel, r.RoleLabel, fmt.Sprintf("%d", n))
	if err != nil {
		return false, err
	}
	if mediaIP == "" {
		cell.Waiting = "media node has no ExternalIP yet"
		return true, nil
	}
	host := cell.Host
	if host == "" {
		if cells.HostNeedsIP(cs.Spec.Host.Template) {
			ip, err := r.loadBalancerIP(ctx, cell.Name)
			if err != nil {
				return false, err
			}
			if ip == "" {
				// Install (or keep) the provisional release so the LB gets created.
				deployed, _, err := r.HelmClient.Deployed(cell.Name)
				if err != nil {
					return false, err
				}
				if !deployed {
					vals := cells.Values(frozen, n, "pending.invalid", mediaIP, r.VPCCIDR)
					if err := r.HelmClient.Apply(cell.Name, vals); err != nil {
						return false, err
					}
				}
				cell.Waiting = "LoadBalancer IP"
				return true, nil
			}
			if host, err = cells.RenderHost(cs.Spec.Host.Template, n, ip); err != nil {
				return false, err
			}
		} else if host, err = cells.RenderHost(cs.Spec.Host.Template, n, ""); err != nil {
			return false, err
		}
		cell.Host = host
	}

	// 3. The release at the final host, applied only when the deployed one differs (an
	// upgrade every requeue would pile helm revisions), then workloads Ready, DNS, helm test.
	deployed, deployedHost, err := r.HelmClient.Deployed(cell.Name)
	if err != nil {
		return false, err
	}
	if !deployed || deployedHost != host {
		vals := cells.Values(frozen, n, host, mediaIP, r.VPCCIDR)
		if err := r.HelmClient.Apply(cell.Name, vals); err != nil {
			return false, err
		}
	}
	if ok, waiting, err := r.workloadsReady(ctx, cell.Name); err != nil {
		return false, err
	} else if !ok {
		cell.Waiting = waiting
		return true, nil
	}
	// DNS before helm test: bbb-web downloads default.pdf from the public host on the
	// first create (spec, "Subir uma célula").
	ip, err := r.loadBalancerIP(ctx, cell.Name)
	if err != nil {
		return false, err
	}
	addrs, err := r.LookupHost(ctx, host)
	if err != nil || !contains(addrs, ip) {
		cell.Waiting = fmt.Sprintf("DNS: %s not resolving to %s", host, ip)
		return true, nil
	}
	if err := r.HelmClient.Test(cell.Name); err != nil {
		cell.Waiting = "helm test: " + err.Error()
		return true, nil
	}
	cell.Phase = bbbv1alpha1.CellReady
	cell.PhaseSince = metav1.Now()
	cell.Waiting = ""
	logger.Info("cell ready", "cell", cell.Name, "host", host)
	return true, nil
}

// register puts a Ready cell into rotation. The same provision deadline applies.
func (r *CellSetReconciler) register(ctx context.Context, cs *bbbv1alpha1.CellSet, cell *bbbv1alpha1.CellStatus) (bool, error) {
	if time.Since(cell.PhaseSince.Time) > provisionTimeout(cs) {
		cell.Phase = bbbv1alpha1.CellFailed
		cell.Reason = fmt.Sprintf("not Registered within %s: %s", provisionTimeout(cs), cell.Waiting)
		return false, nil
	}
	b, err := r.MakeBalancer(ctx, r, cs)
	if err != nil {
		return false, err
	}
	salt, err := cells.Salt(ctx, r.Client, cell.Name)
	if err != nil {
		return false, err
	}
	if err := b.Register(ctx, cells.NewBBBClient(cell.Host, salt, false).APIURL(), salt); err != nil {
		cell.Waiting = "register: " + err.Error()
		return true, nil
	}
	cell.Phase = bbbv1alpha1.CellRegistered
	cell.PhaseSince = metav1.Now()
	cell.Waiting = ""
	log.FromContext(ctx).Info("cell registered", "cell", cell.Name)
	return false, nil
}

// drainGrace is how long a Retiring cell must stay observed-quiet before it is torn down:
// the recorded/ended markers of a meeting that JUST finished take a moment to materialize,
// and an instant read right after the suspension misses them (measured live: a recording
// one second into its pipeline died with the cell twice).
const drainGrace = 2 * time.Minute

// retire finishes a Retiring cell: zero meetings, recordings delivered, the balancer itself
// at zero load, then deregister, uninstall, volumes gone. No deadline: the status says what
// it is waiting for and the operator decides (spec, "Retirar uma célula").
func (r *CellSetReconciler) retire(ctx context.Context, cs *bbbv1alpha1.CellSet, cell *bbbv1alpha1.CellStatus) (bool, error) {
	logger := log.FromContext(ctx)
	deployed, _, err := r.HelmClient.Deployed(cell.Name)
	if err != nil {
		return false, err
	}
	if deployed && cell.Host == "" {
		// Retired straight out of early Provisioning: only the provisional release exists,
		// the cell never served and was never registered - nothing to drain, nothing to
		// deliver, no balancer entry. Straight to uninstall (a meetings read against an
		// empty host would error forever and block every later retirement).
		if err := r.HelmClient.Uninstall(cell.Name); err != nil {
			return false, err
		}
		logger.Info("cell uninstalled", "cell", cell.Name)
		deployed = false
	}
	if deployed {
		if since := time.Since(cell.PhaseSince.Time); since < drainGrace {
			cell.Waiting = fmt.Sprintf("drain grace (%s of %s)", since.Round(time.Second), drainGrace)
			return true, nil
		}
		salt, err := cells.Salt(ctx, r.Client, cell.Name)
		if err != nil {
			return false, err
		}
		api := cells.NewBBBClient(cell.Host, salt, false)
		meetings, participants, err := r.Meetings(ctx, cell.Host, salt)
		if err != nil {
			return false, err
		}
		if meetings > 0 {
			cell.Waiting = fmt.Sprintf("meetings=%d participants=%d", meetings, participants)
			return true, nil
		}
		// "Recordings delivered" only means something with delivery on: without a spool
		// nothing ever writes the sender.done the check reads.
		if delivery, err := r.deliveryEnabled(ctx, cs); err != nil {
			return false, err
		} else if delivery {
			pending, err := r.PendingRecordings(ctx, cell.Name)
			if err != nil {
				return false, err
			}
			if pending > 0 {
				cell.Waiting = fmt.Sprintf("recordings pending delivery=%d", pending)
				return true, nil
			}
		}
		b, err := r.MakeBalancer(ctx, r, cs)
		if err != nil {
			return false, err
		}
		// Deregister only once the balancer itself reports zero load.
		sv, err := b.Lookup(ctx, api.APIURL())
		if err != nil {
			return false, err
		}
		if sv != nil {
			if sv.Load > 0 {
				cell.Waiting = fmt.Sprintf("balancer load=%v", sv.Load)
				return true, nil
			}
			if err := b.Deregister(ctx, api.APIURL()); err != nil {
				return false, err
			}
			logger.Info("cell deregistered", "cell", cell.Name)
		}
		if err := r.HelmClient.Uninstall(cell.Name); err != nil {
			return false, err
		}
		logger.Info("cell uninstalled", "cell", cell.Name)
	}
	// Volumes gone (Delete class), then the namespace, then Retired.
	var pvcs corev1.PersistentVolumeClaimList
	if err := r.List(ctx, &pvcs, client.InNamespace(cell.Name)); err != nil {
		return false, err
	}
	if len(pvcs.Items) > 0 {
		cell.Waiting = fmt.Sprintf("volumes=%d", len(pvcs.Items))
		return true, nil
	}
	var ns corev1.Namespace
	err = r.Get(ctx, types.NamespacedName{Name: cell.Name}, &ns)
	switch {
	case apierrors.IsNotFound(err):
	case err != nil:
		return false, err
	case ns.DeletionTimestamp == nil:
		if err := r.Delete(ctx, &ns); err != nil && !apierrors.IsNotFound(err) {
			return false, err
		}
		cell.Waiting = "namespace terminating"
		return true, nil
	default:
		cell.Waiting = "namespace terminating"
		return true, nil
	}
	cell.Phase = bbbv1alpha1.CellRetired
	cell.Waiting = ""
	logger.Info("cell retired", "cell", cell.Name)
	return false, nil
}

// deliveryEnabled says whether the frozen values switch recording delivery on
// (recording.scalelite.spool.host non-empty).
func (r *CellSetReconciler) deliveryEnabled(ctx context.Context, cs *bbbv1alpha1.CellSet) (bool, error) {
	frozen, err := cells.FrozenValues(ctx, r.Client, cs.Namespace, cs.Spec.Version.ValuesRef)
	if err != nil {
		return false, err
	}
	if rec, ok := frozen["recording"].(map[string]any); ok {
		if sl, ok := rec["scalelite"].(map[string]any); ok {
			if spool, ok := sl["spool"].(map[string]any); ok {
				if host, ok := spool["host"].(string); ok && host != "" {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

// recordingSecretName reads, from the frozen values, the Secret name the chart expects for
// the recording delivery key (recording.scalelite.sshKeySecret).
func (r *CellSetReconciler) recordingSecretName(ctx context.Context, cs *bbbv1alpha1.CellSet) (string, error) {
	if cs.Spec.RecordingSecretRef == "" {
		return "", nil
	}
	frozen, err := cells.FrozenValues(ctx, r.Client, cs.Namespace, cs.Spec.Version.ValuesRef)
	if err != nil {
		return "", err
	}
	if rec, ok := frozen["recording"].(map[string]any); ok {
		if sl, ok := rec["scalelite"].(map[string]any); ok {
			if name, ok := sl["sshKeySecret"].(string); ok && name != "" {
				return name, nil
			}
		}
	}
	return "scalelite-spool-ssh", nil
}

func (r *CellSetReconciler) loadBalancerIP(ctx context.Context, cellNS string) (string, error) {
	var svc corev1.Service
	err := r.Get(ctx, types.NamespacedName{Namespace: cellNS, Name: "bbb-nginx"}, &svc)
	if apierrors.IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	for _, ing := range svc.Status.LoadBalancer.Ingress {
		if ing.IP != "" {
			return ing.IP, nil
		}
	}
	return "", nil
}

// workloadsReady: every Deployment and StatefulSet of the cell at its desired replicas.
func (r *CellSetReconciler) workloadsReady(ctx context.Context, cellNS string) (bool, string, error) {
	var deps appsv1.DeploymentList
	if err := r.List(ctx, &deps, client.InNamespace(cellNS)); err != nil {
		return false, "", err
	}
	var sts appsv1.StatefulSetList
	if err := r.List(ctx, &sts, client.InNamespace(cellNS)); err != nil {
		return false, "", err
	}
	if len(deps.Items) == 0 {
		return false, "no workloads yet", nil
	}
	for _, d := range deps.Items {
		want := int32(1)
		if d.Spec.Replicas != nil {
			want = *d.Spec.Replicas
		}
		if d.Status.ReadyReplicas < want {
			return false, fmt.Sprintf("deployment %s %d/%d", d.Name, d.Status.ReadyReplicas, want), nil
		}
	}
	for _, s := range sts.Items {
		want := int32(1)
		if s.Spec.Replicas != nil {
			want = *s.Spec.Replicas
		}
		if s.Status.ReadyReplicas < want {
			return false, fmt.Sprintf("statefulset %s %d/%d", s.Name, s.Status.ReadyReplicas, want), nil
		}
	}
	return true, "", nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// DefaultLookupHost resolves on the system resolver.
func DefaultLookupHost(ctx context.Context, host string) ([]string, error) {
	return net.DefaultResolver.LookupHost(ctx, host)
}

// SetupWithManager wires the reconciler.
func (r *CellSetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&bbbv1alpha1.CellSet{}).
		Named("cellset").
		Complete(r)
}

// DefaultMeetings reads the cell's own BBB API.
func DefaultMeetings(ctx context.Context, host, salt string) (int, int, error) {
	return cells.NewBBBClient(host, salt, false).Meetings(ctx)
}
