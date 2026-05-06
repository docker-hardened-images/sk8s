package sk8s

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/testcontainers/testcontainers-go/exec"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

func (c *TestCluster) Exec(ctx context.Context, cmd []string, options ...exec.ProcessOption) (int, io.Reader, error) {
	return c.cluster.Exec(ctx, cmd, options...)
}

func (c *TestCluster) ExecPod(ctx context.Context, namespace string, pod string, cmd []string) (bytes.Buffer, bytes.Buffer, error) {
	return c.ExecPodContainer(ctx, namespace, pod, "", cmd)
}

func (c *TestCluster) ExecPodContainer(ctx context.Context, namespace string, pod string, container string, cmd []string) (bytes.Buffer, bytes.Buffer, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	req := c.Client().CoreV1().RESTClient().Post().Resource("pods").Namespace(namespace).Name(pod).SubResource("exec")
	option := &corev1.PodExecOptions{
		Container: container,
		Command:   cmd,
		Stdin:     false,
		Stdout:    true,
		Stderr:    true,
		TTY:       false,
	}
	req.VersionedParams(
		option,
		scheme.ParameterCodec,
	)

	config, err := c.getClusterConfig(ctx)
	if err != nil {
		return stdout, stderr, err
	}

	exec, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		return stdout, stderr, err
	}

	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: bufio.NewWriter(&stdout),
		Stderr: bufio.NewWriter(&stderr),
		Tty:    false,
	})
	return stdout, stderr, err
}

// Execute a job running a pod with a given command
func (c *TestCluster) RunJob(ctx context.Context, namespace string, job *batchv1.Job) (string, error) {
	_, err := c.Client().BatchV1().Jobs(namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to create job: %w", err)
	}

	defer func() {
		deleteCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		propagation := metav1.DeletePropagationForeground
		err = c.Client().BatchV1().Jobs(namespace).Delete(deleteCtx, job.Name, metav1.DeleteOptions{
			PropagationPolicy: &propagation,
		})
		if err != nil {
			return
		}

		watcher, err := c.Client().BatchV1().Jobs(namespace).Watch(deleteCtx, metav1.ListOptions{
			FieldSelector: "metadata.name=" + job.Name,
		})
		if err != nil {
			return
		}
		defer watcher.Stop()

		for event := range watcher.ResultChan() {
			if event.Type == watch.Deleted {
				return
			}
		}
	}()

	// Wait for job to complete
	err = c.WaitForJob(ctx, namespace, job.Name)
	if err != nil {
		return "", err
	}

	// Find the pod the job created
	podList, err := c.Client().CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "job-name=" + job.Name,
	})
	if err != nil {
		return "", fmt.Errorf("failed to list pods for job: %w", err)
	}
	if len(podList.Items) == 0 {
		return "", fmt.Errorf("no pods found for job %s", job.Name)
	}

	// Get logs from the pod
	req := c.Client().CoreV1().Pods(namespace).GetLogs(podList.Items[0].Name, &corev1.PodLogOptions{})
	logs, err := req.Stream(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get logs: %w", err)
	}
	defer func() { _ = logs.Close() }()

	buf, err := io.ReadAll(logs)
	if err != nil {
		return "", err
	}

	return string(buf), nil
}

// GetRaw performs a raw GET request to the Kubernetes API, equivalent to 'kubectl get --raw <apiPath>'
func (c *TestCluster) GetRaw(ctx context.Context, apiPath string) (string, error) {
	// Use the discovery REST client which is designed for making arbitrary API requests
	restClient := c.Client().Discovery().RESTClient()

	// Perform the raw GET request
	result := restClient.Get().AbsPath(apiPath).Do(ctx)
	if result.Error() != nil {
		return "", fmt.Errorf("failed to get raw API path %s: %w", apiPath, result.Error())
	}

	// Get the response body as bytes
	body, err := result.Raw()
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	return string(body), nil
}

// CreateNamespace creates a namespace if it doesn't exist
func (c *TestCluster) CreateNamespace(ctx context.Context, namespace string) error {
	_, err := c.Client().CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: namespace,
				},
			}
			_, err = c.Client().CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create namespace %s: %w", namespace, err)
			}
			return nil
		}
		return fmt.Errorf("failed to get namespace %s: %w", namespace, err)
	}
	return nil
}

// DeleteResource deletes any Kubernetes resource by name and type
func (c *TestCluster) DeleteResource(ctx context.Context, gvr schema.GroupVersionResource, namespace, name string) error {
	dynamicClient, err := c.DynamicClient(ctx)
	if err != nil {
		return err
	}
	var resourceInterface dynamic.ResourceInterface
	if namespace != "" {
		resourceInterface = dynamicClient.Resource(gvr).Namespace(namespace)
	} else {
		resourceInterface = dynamicClient.Resource(gvr)
	}

	err = resourceInterface.Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to delete resource %s/%s: %w", gvr.String(), name, err)
	}
	return nil
}

// GetPodsForDeployment retrieves pods managed by a specific deployment.
func (c *TestCluster) GetPodsForDeployment(ctx context.Context, namespace string, deploymentName string) ([]corev1.Pod, error) {
	deployment, err := c.Client().AppsV1().Deployments(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get deployment: %w", err)
	}

	labelSelector := metav1.FormatLabelSelector(deployment.Spec.Selector)
	podList, err := c.Client().CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}

	return podList.Items, nil
}

func (c *TestCluster) GetPodsForStatefulSet(ctx context.Context, namespace string, stsName string) ([]corev1.Pod, error) {
	deployment, err := c.Client().AppsV1().StatefulSets(namespace).Get(ctx, stsName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get stateful set: %w", err)
	}

	labelSelector := metav1.FormatLabelSelector(deployment.Spec.Selector)
	podList, err := c.Client().CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}

	return podList.Items, nil
}

func (c *TestCluster) GetPodsForDaemonSet(ctx context.Context, namespace string, dsName string) ([]corev1.Pod, error) {
	deamonset, err := c.Client().AppsV1().DaemonSets(namespace).Get(ctx, dsName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get daemonset set: %w", err)
	}

	labelSelector := metav1.FormatLabelSelector(deamonset.Spec.Selector)
	podList, err := c.Client().CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}

	return podList.Items, nil
}

func (c *TestCluster) CreateMockCert(ctx context.Context, namespace string, certName string, caCrt, tlsCret, tlsKey []byte) error {
	certDef := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: certName,
		},
		Type: "kubernetes.io/tls",
		Data: map[string][]byte{
			"ca.crt":  caCrt,
			"tls.crt": tlsCret,
			"tls.key": tlsKey,
		},
	}

	_, err := c.Client().CoreV1().Secrets(namespace).Create(ctx, certDef, metav1.CreateOptions{})
	if err != nil {
		return err
	}

	return nil
}
