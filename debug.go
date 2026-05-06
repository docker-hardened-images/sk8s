package sk8s

import (
	"context"
	"fmt"
	"log"
	"strings"
	"testing"
	"text/tabwriter"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	goDebugVolume = corev1.Volume{
		Name: "debug",
		VolumeSource: corev1.VolumeSource{
			Image: &corev1.ImageVolumeSource{
				Reference:  "dhi/delve:1.26",
				PullPolicy: corev1.PullNever,
			},
		},
	}
	goDebugVolumeMount = corev1.VolumeMount{
		Name:      "debug",
		MountPath: "/debug",
	}
	goDebugArgs = []string{
		"exec",
		"--listen=0.0.0.0:2345",
		"--headless=true",
		"--api-version=2",
		"--only-same-user=false",
		"--check-go-version=false",
		"--accept-multiclient",
		"--log",
		"--continue",
	}
)

func (c *TestCluster) DumpPodsStatus(ctx context.Context, pods []corev1.Pod) {
	var builder = strings.Builder{}
	w := tabwriter.NewWriter(&builder, 1, 1, 1, ' ', 0)

	containerStatus := func(status corev1.ContainerStatus) {
		if status.State.Terminated != nil {
			fmt.Fprintf(w, "\t%s\t%s\n", status.Name, "Completed")
		} else if status.State.Running != nil {
			fmt.Fprintf(w, "\t%s\t%s\n", status.Name, "Running")
		} else {
			fmt.Fprintf(w, "\t%s\t%s\n", status.Name, status.State.Waiting.Reason)
			fmt.Fprintf(w, "\t%s\n", status.State.Waiting.Message)
		}
	}
	for _, pod := range pods {
		fmt.Fprintf(w, "Pod: %s/%s\tStatus:%s\n", pod.Namespace, pod.Name, pod.Status.Phase)

		if len(pod.Status.InitContainerStatuses) > 0 {
			fmt.Fprintln(w, "Init containers:")
			for _, ctrStatus := range pod.Status.InitContainerStatuses {
				containerStatus(ctrStatus)
			}
		}

		if len(pod.Status.ContainerStatuses) > 0 {
			fmt.Fprintln(w, "Containers:")
			for _, ctrStatus := range pod.Status.ContainerStatuses {
				containerStatus(ctrStatus)
			}
		}

		events, err := c.Client().CoreV1().Events(pod.Namespace).List(ctx, metav1.ListOptions{
			FieldSelector: fmt.Sprintf("involvedObject.name=%s,involvedObject.namespace=%s", pod.Name, pod.Namespace),
		})
		if err == nil && len(events.Items) > 0 {
			fmt.Fprintln(w, "Events:")
			fmt.Fprintln(w, "TYPE\tREASON\tMESSAGE")
			for _, e := range events.Items {
				fmt.Fprintf(w, "\t\t%s\t%s\t%s\n", e.Type, e.Reason, e.Message)
			}
		}
	}

	err := w.Flush()
	if err == nil {
		logger := log.Default()
		logger.Println(builder.String())
		builder.Reset()
	}
}

// To use in tests add:
//
//	err = sk8s.SetupClusterGoDebug(t, cluster, "1.25")
//	require.NoError(t, err)
func (c *TestCluster) SetupGoDebug(t *testing.T, delveVersion string) error {
	delveImage := "dhi/delve:" + delveVersion

	err := c.LoadImages(
		t.Context(),
		delveImage,
	)

	return err
}

// TODO: make debug image customizable
// DebugGoDeployment modifies a deployment to image mount the go debugger and replaces the command and argument with the debugging one.
// Example: dhik8s.DebugGoDeployment(t, cluster, client, "default", "my-deployment", "/usr/local/bin/my-cmd")
func (c *TestCluster) DebugGoDeployment(t *testing.T, namespace string, deploymentName string, cmd string) error {
	err := c.SetupGoDebug(t, "1.26")
	if err != nil {
		return err
	}

	deployment, err := c.Client().AppsV1().Deployments(namespace).Get(t.Context(), deploymentName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes, goDebugVolume)
	deployment.Spec.Template.Spec.Containers[0].VolumeMounts = append(deployment.Spec.Template.Spec.Containers[0].VolumeMounts, goDebugVolumeMount)
	deployment.Spec.Template.Spec.Containers[0].Command = []string{"/debug/usr/local/bin/dlv"}

	args := append([]string{}, goDebugArgs...)
	args = append(args, cmd, "--")
	args = append(args, deployment.Spec.Template.Spec.Containers[0].Args...)

	deployment.Spec.Template.Spec.Containers[0].Args = args

	_, err = c.Client().AppsV1().Deployments(namespace).Update(t.Context(), deployment, metav1.UpdateOptions{})
	if err != nil {
		return err
	}

	// Add automatic service to debug port
	var debugService = &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "debug-service-external",
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeLoadBalancer,
			Selector: deployment.Spec.Selector.MatchLabels,
			Ports: []corev1.ServicePort{
				{
					Name:     "debug",
					Port:     2345,
					NodePort: 32345,
				},
			},
		},
	}
	_, err = c.Client().CoreV1().Services(namespace).Create(t.Context(), debugService, metav1.CreateOptions{})
	if err != nil {
		return err
	}

	return nil
}
