package cells

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"text/template"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

// RenderHost renders the host template over {{.index}} and {{.ip}} (IP with dashes).
// With {{.ip}} and no IP yet, the caller installs with a provisional host first.
func RenderHost(tmpl string, index int32, ip string) (string, error) {
	t, err := template.New("host").Option("missingkey=error").Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("host template: %w", err)
	}
	var out bytes.Buffer
	err = t.Execute(&out, map[string]string{
		"index": fmt.Sprintf("%d", index),
		"ip":    strings.ReplaceAll(ip, ".", "-"),
	})
	return out.String(), err
}

// HostNeedsIP says whether the template depends on the LoadBalancer IP.
func HostNeedsIP(tmpl string) bool { return strings.Contains(tmpl, ".ip") }

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// EnsureNamespace creates the cell's namespace when missing.
func EnsureNamespace(ctx context.Context, c client.Client, name string) error {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if err := c.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

// EnsureSecrets is what scripts/create-secrets.sh does per namespace, done per cell
// (phase 2 spec, "Secrets imperativos"): the generated ones are created once and kept, the
// copied ones (TLS, recording credential) are refreshed from the source namespace.
func EnsureSecrets(ctx context.Context, c client.Client, cellNS, srcNS, tlsRef, recordingRef, recordingName string) error {
	generated := []corev1.Secret{
		{ObjectMeta: metav1.ObjectMeta{Name: "bbb-db", Namespace: cellNS},
			StringData: map[string]string{
				"postgres-password": randHex(24), "bbb-core-password": randHex(24),
				"bbb-hasura-password": randHex(24), "hasura-app-password": randHex(24),
				"blocknote-app-password": randHex(24)}},
		{ObjectMeta: metav1.ObjectMeta{Name: "bbb-salt", Namespace: cellNS},
			StringData: map[string]string{"security-salt": randHex(24)}},
		{ObjectMeta: metav1.ObjectMeta{Name: "bbb-hasura", Namespace: cellNS},
			StringData: map[string]string{"admin-secret": randHex(24)}},
	}
	lkSecret := randHex(32)
	generated = append(generated, corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "bbb-livekit", Namespace: cellNS},
		StringData: map[string]string{"api-secret": lkSecret, "keys.yaml": "bbbk8s: " + lkSecret}})
	for i := range generated {
		if err := c.Create(ctx, &generated[i]); err != nil && !apierrors.IsAlreadyExists(err) {
			return err
		}
	}
	copies := map[string]string{tlsRef: "bbb-tls"}
	if recordingRef != "" {
		copies[recordingRef] = recordingName
	}
	for src, dst := range copies {
		var s corev1.Secret
		if err := c.Get(ctx, types.NamespacedName{Namespace: srcNS, Name: src}, &s); err != nil {
			return fmt.Errorf("source secret %s/%s: %w", srcNS, src, err)
		}
		out := corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: dst, Namespace: cellNS},
			Type: s.Type, Data: s.Data}
		if err := c.Create(ctx, &out); err != nil {
			if !apierrors.IsAlreadyExists(err) {
				return err
			}
			var cur corev1.Secret
			if err := c.Get(ctx, types.NamespacedName{Namespace: cellNS, Name: dst}, &cur); err != nil {
				return err
			}
			cur.Data = s.Data
			cur.Type = s.Type
			if err := c.Update(ctx, &cur); err != nil {
				return err
			}
		}
	}
	return nil
}

// Salt reads the cell's shared secret, for the register hook and the cell API reads.
func Salt(ctx context.Context, c client.Client, cellNS string) (string, error) {
	var s corev1.Secret
	if err := c.Get(ctx, types.NamespacedName{Namespace: cellNS, Name: "bbb-salt"}, &s); err != nil {
		return "", err
	}
	return string(s.Data["security-salt"]), nil
}

// FrozenValues loads the CellSet's frozen values.yaml (spec.version.valuesRef, key values.yaml).
func FrozenValues(ctx context.Context, c client.Client, ns, ref string) (map[string]any, error) {
	var cm corev1.ConfigMap
	if err := c.Get(ctx, types.NamespacedName{Namespace: ns, Name: ref}, &cm); err != nil {
		return nil, err
	}
	raw, ok := cm.Data["values.yaml"]
	if !ok {
		return nil, fmt.Errorf("ConfigMap %s/%s has no values.yaml key", ns, ref)
	}
	out := map[string]any{}
	if err := yaml.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("values.yaml of %s/%s: %w", ns, ref, err)
	}
	return out, nil
}

// MediaNode finds the cell's media node (labels bbb-k8s/cell=<n> and bbb-k8s/role=media)
// and returns its external and internal IPs. The node pool is the operator's: a missing
// node is "not yet", not an error.
func MediaNode(ctx context.Context, c client.Client, cellLabel, roleLabel, cell string) (external, internal string, err error) {
	var nodes corev1.NodeList
	if err = c.List(ctx, &nodes, client.MatchingLabels{cellLabel: cell, roleLabel: "media"}); err != nil {
		return "", "", err
	}
	if len(nodes.Items) == 0 {
		return "", "", nil
	}
	for _, a := range nodes.Items[0].Status.Addresses {
		switch a.Type {
		case corev1.NodeExternalIP:
			external = a.Address
		case corev1.NodeInternalIP:
			internal = a.Address
		}
	}
	return external, internal, nil
}

// CellNodesReady says whether the cell has at least one node per role, the operator's
// prerequisite for bringing the cell up.
func CellNodesReady(ctx context.Context, c client.Client, cellLabel, roleLabel, cell string) (bool, []string, error) {
	missing := []string{}
	for _, role := range []string{"storage", "media", "control", "data"} {
		var nodes corev1.NodeList
		if err := c.List(ctx, &nodes, client.MatchingLabels{cellLabel: cell, roleLabel: role}); err != nil {
			return false, nil, err
		}
		if len(nodes.Items) == 0 {
			missing = append(missing, role)
		}
	}
	return len(missing) == 0, missing, nil
}

// Values composes the cell's values: the frozen copy plus what the controller owns per cell
// (phase 2 spec, "Valores próprios de cada célula").
func Values(frozen map[string]any, cell int32, host, mediaExternalIP, vpcCIDR string) map[string]any {
	out := map[string]any{}
	for k, v := range frozen {
		out[k] = v
	}
	out["cell"] = fmt.Sprintf("%d", cell)
	out["publicHost"] = host
	includes := []any{mediaExternalIP + "/32"}
	if vpcCIDR != "" {
		includes = append(includes, vpcCIDR)
	}
	lk, _ := out["livekit"].(map[string]any)
	if lk == nil {
		lk = map[string]any{}
	} else {
		cp := map[string]any{}
		for k, v := range lk {
			cp[k] = v
		}
		lk = cp
	}
	lk["nodeIP"] = mediaExternalIP
	cfg, _ := lk["config"].(map[string]any)
	if cfg == nil {
		cfg = map[string]any{}
	}
	rtc, _ := cfg["rtc"].(map[string]any)
	if rtc == nil {
		rtc = map[string]any{}
	}
	rtc["ips"] = map[string]any{"includes": includes}
	cfg["rtc"] = rtc
	lk["config"] = cfg
	out["livekit"] = lk
	return out
}
