// Package cells brings one cell up and down: Helm release, per-cell Secrets and values,
// and the reads retirement needs (meetings on the cell, pending recordings).
package cells

import (
	"fmt"
	"log/slog"
	"time"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage/driver"
	"k8s.io/cli-runtime/pkg/genericclioptions"
)

const ReleaseName = "bbb"

// Helm runs the chart the controller was built with (phase 2 spec: a cell's version is the
// chart at a commit; the controller carries that chart and refuses another version).
type Helm struct {
	ChartPath string
}

func NewHelm(chartPath string) *Helm {
	return &Helm{ChartPath: chartPath}
}

func (h *Helm) Chart() (*chart.Chart, error) {
	return loader.Load(h.ChartPath)
}

// config builds an action configuration whose client getter is SCOPED to the cell's
// namespace: manifests without a namespace of their own land there, as `helm -n` does.
// (Measured live: a getter left on the kubeconfig default sent a whole cell into
// "default", whatever install.Namespace said.)
func (h *Helm) config(namespace string) (*action.Configuration, error) {
	flags := genericclioptions.NewConfigFlags(false)
	ns := namespace
	flags.Namespace = &ns
	cfg := new(action.Configuration)
	if err := cfg.Init(flags, namespace, "secret",
		func(format string, v ...interface{}) { slog.Debug(fmt.Sprintf(format, v...)) }); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Status returns the deployed release in the namespace, or nil when none exists.
func (h *Helm) Status(namespace string) (*release.Release, error) {
	cfg, err := h.config(namespace)
	if err != nil {
		return nil, err
	}
	rel, err := action.NewGet(cfg).Run(ReleaseName)
	if err == driver.ErrReleaseNotFound {
		return nil, nil
	}
	return rel, err
}

// Apply installs or upgrades the release with the merged values. It does not wait:
// readiness is the reconciler's own check (workloadsReady), so the status stays live and a
// hung API call cannot pin a reconcile for minutes (seen live: a create call stalled to the
// 10-minute deadline under Wait).
func (h *Helm) Apply(namespace string, values map[string]any, timeout time.Duration) error {
	ch, err := h.Chart()
	if err != nil {
		return err
	}
	cfg, err := h.config(namespace)
	if err != nil {
		return err
	}
	if rel, err := h.Status(namespace); err != nil {
		return err
	} else if rel == nil {
		inst := action.NewInstall(cfg)
		inst.ReleaseName = ReleaseName
		inst.Namespace = namespace
		inst.Timeout = timeout
		_, err = inst.Run(ch, values)
		return err
	}
	up := action.NewUpgrade(cfg)
	up.Namespace = namespace
	up.ResetValues = true
	up.Timeout = timeout
	_, err = up.Run(ReleaseName, ch, values)
	return err
}

// Test runs helm test (api-smoke: create, join, getMeetingInfo, end through the public host).
func (h *Helm) Test(namespace string, timeout time.Duration) error {
	cfg, err := h.config(namespace)
	if err != nil {
		return err
	}
	t := action.NewReleaseTesting(cfg)
	t.Namespace = namespace
	t.Timeout = timeout
	_, err = t.Run(ReleaseName)
	return err
}

// Uninstall removes the release; a missing release is a no-op (idempotent resume).
func (h *Helm) Uninstall(namespace string) error {
	cfg, err := h.config(namespace)
	if err != nil {
		return err
	}
	un := action.NewUninstall(cfg)
	un.Wait = true
	un.Timeout = 10 * time.Minute
	_, err = un.Run(ReleaseName)
	if err == driver.ErrReleaseNotFound {
		return nil
	}
	return err
}

// SDK adapts Helm to the reconciler's interface with fixed PoC timeouts.
type SDK struct{ *Helm }

func (s SDK) ChartVersion() (string, error) {
	ch, err := s.Chart()
	if err != nil {
		return "", err
	}
	return ch.Metadata.Version, nil
}

func (s SDK) Deployed(namespace string) (bool, string, error) {
	rel, err := s.Status(namespace)
	if rel == nil || err != nil {
		return false, "", err
	}
	host, _ := rel.Config["publicHost"].(string)
	return true, host, nil
}

func (s SDK) Apply(namespace string, values map[string]any) error {
	return s.Helm.Apply(namespace, values, 10*time.Minute)
}

func (s SDK) Test(namespace string) error {
	return s.Helm.Test(namespace, 5*time.Minute)
}
