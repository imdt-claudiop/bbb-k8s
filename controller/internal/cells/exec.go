package cells

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

// PendingRecordings counts the cell's recordings not yet delivered (phase 2 spec,
// "Condição de Retired"): every recorded meeting without its <id>-sender.done (the scalelite
// post_publish writes it after handing the tar over) that did not end as .norecord, plus any
// tar still in the local handoff spool. A recording still crossing the pipeline counts - the
// first live run retired a cell while one was processing and the recording died with it. It
// runs in the recording pod; no recording pod running means nothing can be pending.
func PendingRecordings(ctx context.Context, cfg *rest.Config, cs kubernetes.Interface, cellNS string) (int, error) {
	pods, err := cs.CoreV1().Pods(cellNS).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=recording"})
	if err != nil {
		return 0, err
	}
	var pod *corev1.Pod
	for i := range pods.Items {
		if pods.Items[i].Status.Phase == corev1.PodRunning {
			pod = &pods.Items[i]
			break
		}
	}
	if pod == nil {
		return 0, nil
	}
	req := cs.CoreV1().RESTClient().Post().Resource("pods").Name(pod.Name).
		Namespace(cellNS).SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: "resque-worker",
			Command: []string{"sh", "-c", `p=0
for f in /var/bigbluebutton/recording/status/recorded/*.done; do
  [ -e "$f" ] || continue
  id=$(basename "$f" .done)
  [ -e "/var/bigbluebutton/recording/status/published/$id-sender.done" ] && continue
  [ -e "/var/bigbluebutton/recording/status/archived/$id.norecord" ] && continue
  p=$((p+1))
done
t=$(ls /var/bigbluebutton/spool 2>/dev/null | wc -l)
echo $((p+t))`},
			Stdout: true, Stderr: true,
		}, scheme.ParameterCodec)
	exec, err := remotecommand.NewSPDYExecutor(cfg, "POST", req.URL())
	if err != nil {
		return 0, err
	}
	var stdout, stderr bytes.Buffer
	if err := exec.StreamWithContext(ctx, remotecommand.StreamOptions{Stdout: &stdout, Stderr: &stderr}); err != nil {
		return 0, fmt.Errorf("exec in %s/%s: %w: %s", cellNS, pod.Name, err, stderr.String())
	}
	n := 0
	fmt.Sscanf(strings.TrimSpace(stdout.String()), "%d", &n)
	return n, nil
}
