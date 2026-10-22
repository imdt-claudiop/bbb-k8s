package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CellSetSpec describes a set of cells of one version (phase 2 spec).
type CellSetSpec struct {
	// Cells that should serve, written by the operator (or an autoscaler through the scale
	// subresource).
	// +kubebuilder:validation:Minimum=0
	Replicas *int32 `json:"replicas"`

	// How long Provisioning or Ready may take before the cell is Failed. Default 20m.
	// +kubebuilder:default="20m"
	ProvisionTimeout metav1.Duration `json:"provisionTimeout,omitempty"`

	Version  VersionSpec  `json:"version"`
	Host     HostSpec     `json:"host"`
	Balancer BalancerSpec `json:"balancer"`

	// Recording delivery credential, copied to every cell as the Secret named by the frozen
	// values (recording.scalelite.sshKeySecret); common to all cells, like the TLS one.
	// Empty: nothing copied.
	RecordingSecretRef string `json:"recordingSecretRef,omitempty"`
}

// VersionSpec pins the cell version: the chart at a commit plus the frozen values.
type VersionSpec struct {
	// Chart version the cells run. The controller carries the chart of the commit it was
	// built from and refuses a mismatch (publishing charts to a registry is later work).
	Chart string `json:"chart"`
	// ConfigMap (key values.yaml) with the frozen copy of the values, in the CellSet's
	// namespace.
	ValuesRef string `json:"valuesRef"`
}

// HostSpec shapes each cell's public host.
type HostSpec struct {
	// Go template over {{.index}} (the cell number) and {{.ip}} (the cell's LoadBalancer IP
	// with dashes). With {{.ip}}, the cell is installed with a provisional host first and
	// upgraded once the IP exists, as in phase 1.
	Template string `json:"template"`
	// TLS Secret (kubernetes.io/tls) in the CellSet's namespace covering the cells' hosts;
	// copied to each cell.
	TLSSecretRef string `json:"tlsSecretRef"`
}

// BalancerSpec names the balancer in front of the cells.
type BalancerSpec struct {
	// +kubebuilder:validation:Enum=scalelite
	Kind string `json:"kind"`
	// Base URL of the balancer's admin API (scalelite: https://host/scalelite/api).
	URL string `json:"url"`
	// Secret in the CellSet's namespace with the API credential (scalelite: key
	// loadbalancer-secret). Never copied to cells.
	SecretRef string `json:"secretRef"`
}

// CellPhase is a cell's lifecycle state. Six states, no way back after Retiring.
// +kubebuilder:validation:Enum=Provisioning;Ready;Registered;Retiring;Retired;Failed
type CellPhase string

const (
	CellProvisioning CellPhase = "Provisioning"
	CellReady        CellPhase = "Ready"
	CellRegistered   CellPhase = "Registered"
	CellRetiring     CellPhase = "Retiring"
	CellRetired      CellPhase = "Retired"
	CellFailed       CellPhase = "Failed"
)

// CellStatus is one cell as the controller last saw it.
type CellStatus struct {
	// Name is the cell's namespace (cell-<n>), never reused.
	Name  string    `json:"name"`
	Phase CellPhase `json:"phase"`
	// Public host, once rendered.
	Host string `json:"host,omitempty"`
	// When the current phase was entered (drives the provision timeout and the retirement
	// tie-break by age uses Created).
	PhaseSince metav1.Time `json:"phaseSince,omitempty"`
	Created    metav1.Time `json:"created,omitempty"`
	// What Retiring still waits for, for the operator ("meetings=2", "recordings pending").
	Waiting string `json:"waiting,omitempty"`
	// Why the cell Failed, for the operator.
	Reason string `json:"reason,omitempty"`
}

// CellSetStatus is the observed state.
type CellSetStatus struct {
	// Cells that serve or will serve: Provisioning, Ready and Registered (not Retiring,
	// Retired or Failed).
	Replicas int32 `json:"replicas"`
	// Selector for the scale subresource (HPA contract).
	Selector string `json:"selector,omitempty"`
	// The number of the next cell to be created: the operator labels and taints its node
	// pool (bbb-k8s/cell=<n>) before scaling (decision A: a global, ever-increasing
	// counter; names are never reused).
	NextCell int32 `json:"nextCell,omitempty"`
	// Cells this set owns, Retired ones pruned.
	Cells []CellStatus `json:"cells,omitempty"`
	// Human-readable refusals and notes (e.g. the refusal to zero the only registered set).
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=cs
// +kubebuilder:subresource:status
// +kubebuilder:subresource:scale:specpath=.spec.replicas,statuspath=.status.replicas,selectorpath=.status.selector
// +kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=".spec.replicas"
// +kubebuilder:printcolumn:name="Serving",type=integer,JSONPath=".status.replicas"
// +kubebuilder:printcolumn:name="Next",type=integer,JSONPath=".status.nextCell"

// CellSet is a set of BigBlueButton cells of one version behind one balancer.
type CellSet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CellSetSpec   `json:"spec,omitempty"`
	Status CellSetStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// CellSetList contains a list of CellSet.
type CellSetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CellSet `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CellSet{}, &CellSetList{})
}
