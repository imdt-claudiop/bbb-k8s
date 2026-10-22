// Package balancer is the balancer-agnostic boundary of the controller (phase 2 spec,
// "Hooks"): three hooks, implemented per balancer, plus the load reads retirement needs.
// The state machine never names a concrete balancer; a new kind is a new implementation
// of this interface picked by spec.balancer.kind.
package balancer

import "context"

// Server is a cell as the balancer sees it.
type Server struct {
	ID       string
	Host     string // the cell's API base URL
	State    string // balancer-specific, informative
	Load     float64
	Online   bool
	Meetings int
}

// Balancer is implemented once per spec.balancer.kind.
type Balancer interface {
	// Register adds the cell (host's BBB API URL, its shared secret) and enables it.
	// Idempotent: registering an already-known host re-enables it.
	Register(ctx context.Context, apiURL, secret string) error
	// Suspend takes the cell out of rotation letting existing meetings finish
	// (scalelite: cordon). Idempotent.
	Suspend(ctx context.Context, apiURL string) error
	// Deregister removes the cell. Idempotent: deregistering an unknown host is a no-op.
	Deregister(ctx context.Context, apiURL string) error
	// Lookup returns the server for the cell's API URL, or nil when unknown.
	Lookup(ctx context.Context, apiURL string) (*Server, error)
}
