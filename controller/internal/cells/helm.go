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
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage/driver"
)

const ReleaseName = "bbb"

// Helm runs the chart the controller was built with (phase 2 spec: a cell's version is the
// chart at a commit; the controller carries that chart and refuses another version).
type Helm struct {
	ChartPath string
	settings  *cli.EnvSettings
}

func NewHelm(chartPath string) *Helm {
	return &Helm{ChartPath: chartPath, settings: cli.New()}
}

func (h *Helm) Chart() (*chart.Chart, error) {
	return loader.Load(h.ChartPath)
}

func (h *Helm) config(namespace string) (*action.Configuration, error) {
	cfg := new(action.Configuration)
	if err := cfg.Init(h.settings.RESTClientGetter(), namespace, "secret",
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

// Apply installs or upgrades the release with the merged values, waiting for readiness.
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
		inst.Wait = true
		inst.Timeout = timeout
		_, err = inst.Run(ch, values)
		return err
	}
	up := action.NewUpgrade(cfg)
	up.Namespace = namespace
	up.ResetValues = true
	up.Wait = true
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

func (s SDK) Deployed(namespace string) (bool, error) {
	rel, err := s.Status(namespace)
	return rel != nil, err
}

func (s SDK) Apply(namespace string, values map[string]any) error {
	return s.Helm.Apply(namespace, values, 10*time.Minute)
}

func (s SDK) Test(namespace string) error {
	return s.Helm.Test(namespace, 5*time.Minute)
}
