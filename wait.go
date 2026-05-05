package k8sv2

import (
	"context"
	"fmt"
	"io"
	"log"
	"strings"
	"text/tabwriter"
	"time"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

func (c *TestCluster) WaitFor(ctx context.Context, fn func(ctx context.Context) (bool, error)) error {
	return kwait.PollUntilContextCancel(ctx, time.Second, true, fn)
}

func (c *TestCluster) WaitForWithTimeout(ctx context.Context, interval time.Duration, timeout time.Duration, immediate bool, condition kwait.ConditionWithContextFunc) error {
	return kwait.PollUntilContextTimeout(ctx, interval, timeout, immediate, condition)
}

func (c *TestCluster) WaitForState(ctx context.Context, namespace string, pod string, state string) error {
	return c.WaitForPod(ctx, namespace, pod, state)
}

func (c *TestCluster) WaitForPod(ctx context.Context, namespace string, pod string, state string) error {
	fn := func(ctx context.Context) (bool, error) {
		list, err := c.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
			FieldSelector: "metadata.name=" + pod,
		})
		if err != nil {
			return true, err
		}
		if len(list.Items) == 0 {
			return false, nil
		}

		pod := list.Items[0]

		if len(pod.Status.ContainerStatuses) == 0 {
			return false, nil
		}

		switch state {
		case "ready":
			// Check pod-level ready condition
			for _, condition := range pod.Status.Conditions {
				if condition.Type == corev1.PodReady {
					return condition.Status == corev1.ConditionTrue, nil
				}
			}
			return false, nil
		case "running":
			return pod.Status.ContainerStatuses[0].State.Running != nil, nil
		case "completed":
			return pod.Status.ContainerStatuses[0].State.Terminated != nil, nil
		case "terminated":
			return pod.Status.ContainerStatuses[0].State.Terminated != nil, nil

		}

		return false, fmt.Errorf("state not supported %s", state)
	}

	return c.WaitFor(ctx, fn)
}

func (c *TestCluster) WaitForLog(ctx context.Context, namespace string, pod string, log string) error {
	return c.WaitForLogWithOptions(ctx, namespace, pod, log, &corev1.PodLogOptions{})
}

func (c *TestCluster) WaitForLogWithOptions(ctx context.Context, namespace string, pod string, log string, options *corev1.PodLogOptions) error {
	handleError := func(err error) (bool, error) {
		if options != nil && options.Container != "" {
			return false, nil
		}
		return false, err
	}

	fn := func(ctx context.Context) (bool, error) {
		req := c.Client().CoreV1().Pods(namespace).GetLogs(pod, options)
		logStream, err := req.Stream(ctx)
		if err != nil {
			return handleError(err)
		}
		defer func() {
			_ = logStream.Close()
		}()

		podLogs, err := io.ReadAll(logStream)
		if err != nil {
			return handleError(err)
		}
		// fmt.Print(string(podLogs))
		fmt.Printf("%q\n", string(podLogs))
		return strings.Contains(string(podLogs), log), nil
	}
	return c.WaitFor(ctx, fn)
}

func (c *TestCluster) WaitForLogLineWithOptions(ctx context.Context, namespace string, pod string, log []string, options *corev1.PodLogOptions) error {
	handleError := func(err error) (bool, error) {
		if options != nil && options.Container != "" {
			return false, nil
		}
		return false, err
	}

	fn := func(ctx context.Context) (bool, error) {
		req := c.Client().CoreV1().Pods(namespace).GetLogs(pod, options)
		logStream, err := req.Stream(ctx)
		if err != nil {
			return handleError(err)
		}
		defer func() {
			_ = logStream.Close()
		}()

		podLogs, err := io.ReadAll(logStream)
		if err != nil {
			return handleError(err)
		}

		// Browse log lines until finding one containing all elements of "log"
		lines := strings.Split(string(podLogs), "\n")
		for _, line := range lines {
			containsAll := true
			for _, logElement := range log {
				containsAll = containsAll && strings.Contains(line, logElement)
				if !containsAll {
					continue
				}
			}
			if containsAll {
				return true, nil
			}
		}

		return false, nil
	}
	return c.WaitFor(ctx, fn)
}

func (c *TestCluster) WaitForService(ctx context.Context, namespace string, serviceName string) error {
	fn := func(ctx context.Context) (bool, error) {
		_, err := c.Client().CoreV1().Services(namespace).Get(ctx, serviceName, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}

		endpoints, err := c.Client().CoreV1().Endpoints(namespace).Get(ctx, serviceName, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}

		for _, subset := range endpoints.Subsets {
			if len(subset.Addresses) > 0 {
				// At least one endpoint has an address assigned
				return true, nil
			}
		}

		return false, nil
	}

	return c.WaitFor(ctx, fn)
}

func (c *TestCluster) WaitForJob(ctx context.Context, namespace string, jobName string) error {
	fn := func(ctx context.Context) (bool, error) {
		job, err := c.Client().BatchV1().Jobs(namespace).Get(ctx, jobName, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return false, fmt.Errorf("job not found")
			}
			return false, fmt.Errorf("error getting job: %v", err)
		}

		if job.Status.Failed > 0 {
			c.logFailedJob(ctx, namespace, jobName)
			return false, fmt.Errorf("job failed")
		}
		return job.Status.Succeeded > 0, nil
	}
	return c.WaitFor(ctx, fn)
}

func (c *TestCluster) logFailedJob(ctx context.Context, namespace string, jobName string) {
	logger := log.Default()
	commands := [][]string{
		{"kubectl", "describe", "job", jobName, "-n", namespace},
		{"kubectl", "get", "pods", "-n", namespace, "-l", "job-name=" + jobName, "-o", "wide"},
		{"kubectl", "logs", "job/" + jobName, "-n", namespace, "--all-containers=true"},
	}

	for _, cmd := range commands {
		exitCode, reader, err := c.cluster.Exec(ctx, cmd)
		if err != nil {
			logger.Printf("Failed to run %q: %v", strings.Join(cmd, " "), err)
			continue
		}

		output, readErr := io.ReadAll(reader)
		if readErr != nil {
			logger.Printf("Failed to read %q output: %v", strings.Join(cmd, " "), readErr)
			continue
		}

		if exitCode != 0 {
			logger.Printf("%s exited %d:\n%s", strings.Join(cmd, " "), exitCode, string(output))
			continue
		}

		logger.Printf("%s:\n%s", strings.Join(cmd, " "), string(output))
	}
}

func (c *TestCluster) WaitForDeployment(ctx context.Context, namespace string, deploymentName string) error {
	attempts := 0
	fn := func(ctx context.Context) (bool, error) {
		attempts++

		if attempts%60 == 0 { // One minute and still not ready. Worth logging, specially on CI
			c.logNamespaceResources(ctx, namespace)
		}

		deployment, err := c.Client().AppsV1().Deployments(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}

		return deployment.Status.ReadyReplicas == deployment.Status.Replicas && deployment.Status.Replicas > 0, nil
	}
	return c.WaitFor(ctx, fn)
}

func (c *TestCluster) logNamespaceResources(ctx context.Context, namespace string) {
	logger := log.Default()
	logger.Printf("Deployment still not ready. Checking resources in namespace %s...", namespace)
	exitCode, reader, err := c.cluster.Exec(ctx, []string{
		"kubectl", "get", "all", "-n", namespace,
	})
	if err != nil {
		logger.Printf("Failed to get namespace %s resources: %v", namespace, err)
		return
	}
	output, readErr := io.ReadAll(reader)
	if readErr != nil {
		logger.Printf("Failed to read kubectl output: %v", readErr)
		return
	}
	if exitCode != 0 {
		logger.Printf("kubectl get all failed with exit code %d: %s", exitCode, string(output))
		return
	}
	logger.Printf("Namespace %s resources:\n%s", namespace, string(output))

	// Describe any pods that are not in a healthy state
	c.logUnhealthyPods(ctx, namespace)
}

// logUnhealthyPods describes pods that are not in a healthy state
func (c *TestCluster) logUnhealthyPods(ctx context.Context, namespace string) {
	logger := log.Default()

	pods, err := c.Client().CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return
	}

	for _, pod := range pods.Items {
		phase := pod.Status.Phase
		// Check for unhealthy pods: not Running, not Succeeded, or Running but with container issues
		isUnhealthy := phase != corev1.PodRunning && phase != corev1.PodSucceeded
		if !isUnhealthy && phase == corev1.PodRunning {
			// Check for container issues even if pod shows as Running
			for _, cs := range pod.Status.ContainerStatuses {
				if cs.State.Waiting != nil || (cs.State.Terminated != nil && cs.State.Terminated.ExitCode != 0) {
					isUnhealthy = true
					break
				}
			}
		}

		if isUnhealthy {
			logger.Printf("Describing unhealthy pod %s (phase: %s)...", pod.Name, phase)
			exitCode, reader, err := c.cluster.Exec(ctx, []string{
				"kubectl", "describe", "pod", pod.Name, "-n", namespace,
			})
			if err != nil {
				logger.Printf("Failed to describe pod %s: %v", pod.Name, err)
				continue
			}
			output, _ := io.ReadAll(reader)
			if exitCode == 0 && len(output) > 0 {
				logger.Printf("Pod %s description:\n%s", pod.Name, string(output))
			}
		}
	}
}

// Checks that CRD exists and is in Established state
func (c *TestCluster) WaitForCRD(ctx context.Context, crdName string) error {
	apiExtClient, err := c.ApiExtClient(ctx)
	if err != nil {
		return err
	}
	fn := func(ctx context.Context) (bool, error) {
		crd, err := apiExtClient.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, crdName, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}

		// Check if CRD is established
		for _, condition := range crd.Status.Conditions {
			if condition.Type == apiextensionsv1.Established && condition.Status == apiextensionsv1.ConditionTrue {
				return true, nil
			}
		}
		return false, nil
	}
	return c.WaitFor(ctx, fn)
}

// Checks that an API resource exists
func (c *TestCluster) WaitForAPIResource(ctx context.Context, apiGroup string, resourceName string, kind string) error {
	fn := func(ctx context.Context) (bool, error) {
		// Get all resources from the cluster
		resourceLists, err := c.Client().Discovery().ServerPreferredResources()
		if err != nil {
			// ServerPreferredResources can return partial results with an error
			// We should check if we got any results despite the error
			if resourceLists == nil {
				return false, err
			}
		}

		// Filter by API group if specified
		if apiGroup != "" {
			var filteredLists []*metav1.APIResourceList
			groupPrefix := apiGroup + "/"
			for _, resourceList := range resourceLists {
				if strings.HasPrefix(resourceList.GroupVersion, groupPrefix) {
					filteredLists = append(filteredLists, resourceList)
				}
			}
			resourceLists = filteredLists
		}

		// Check if the resource with the given name and kind exists
		for _, resourceList := range resourceLists {
			for _, resource := range resourceList.APIResources {
				if resource.Name == resourceName && resource.Kind == kind {
					return true, nil
				}
			}
		}

		// Resource not found
		return false, nil
	}
	return c.WaitFor(ctx, fn)
}

func (c *TestCluster) WaitForStatefulSet(ctx context.Context, namespace string, stsName string) error {
	var builder = strings.Builder{}
	w := tabwriter.NewWriter(&builder, 1, 1, 1, ' ', 0)
	attempts := 0

	fn := func(ctx context.Context) (bool, error) {
		attempts++

		if attempts%60 == 0 { // One minute and still not ready. Worth logging, specially on CI
			c.logNamespaceResources(ctx, namespace)
		}

		sts, err := c.Client().AppsV1().StatefulSets(namespace).Get(ctx, stsName, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}

		status := &sts.Status
		ready := status.ReadyReplicas == status.Replicas && status.Replicas > 0

		fmt.Fprintln(w, "\nNAME\tDESIRED\tCURRENT\tREADY")
		fmt.Fprintf(w, "%s\t%d\t%d\t%d", sts.Name, int(status.Replicas), int(status.CurrentReplicas), int(status.ReadyReplicas))
		err = w.Flush()
		if err == nil {
			logger := log.Default()
			logger.Println(builder.String())
			builder.Reset()
		}

		return ready, nil
	}

	return c.WaitFor(ctx, fn)
}

func (c *TestCluster) WaitForDaemonSet(ctx context.Context, namespace string, dsName string) error {
	fn := func(ctx context.Context) (bool, error) {
		ds, err := c.Client().AppsV1().DaemonSets(namespace).Get(ctx, dsName, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}

		return ds.Status.NumberReady == ds.Status.DesiredNumberScheduled && ds.Status.DesiredNumberScheduled > 0, nil
	}
	return c.WaitFor(ctx, fn)
}

func (c *TestCluster) WaitForPodByLabel(ctx context.Context, namespace string, labelSelector string) (*corev1.Pod, error) {
	var pod *corev1.Pod
	fn := func(ctx context.Context) (bool, error) {
		list, err := c.Client().CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		if err != nil {
			return false, err
		}

		if len(list.Items) == 0 {
			return false, nil
		}

		pod = &list.Items[0]
		if pod.Status.Phase != corev1.PodRunning {
			return false, nil
		}

		// Check if containers are ready
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.ContainersReady && condition.Status == corev1.ConditionTrue {
				return true, nil
			}
		}

		return false, nil
	}

	err := c.WaitFor(ctx, fn)
	return pod, err
}

func (c *TestCluster) WaitForPVC(ctx context.Context, namespace string, pvcName string) error {
	fn := func(ctx context.Context) (bool, error) {
		pvc, err := c.Client().CoreV1().PersistentVolumeClaims(namespace).Get(ctx, pvcName, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}

		return pvc.Status.Phase == corev1.ClaimBound, nil
	}
	return c.WaitFor(ctx, fn)
}

func (c *TestCluster) WaitForSecret(ctx context.Context, namespace string, secretName string) error {
	fn := func(ctx context.Context) (bool, error) {
		_, err := c.Client().CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	}
	return c.WaitFor(ctx, fn)
}

func (c *TestCluster) WaitForExternalService(ctx context.Context, namespace string, serviceName string) error {
	fn := func(ctx context.Context) (bool, error) {
		s, err := c.Client().CoreV1().Services(namespace).Get(ctx, serviceName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		if s.Spec.Type != corev1.ServiceTypeLoadBalancer {
			return false, fmt.Errorf("service is not of type LoadBalancer")
		}

		ready := len(s.Status.LoadBalancer.Ingress) > 0 && s.Status.LoadBalancer.Ingress[0].IP != ""

		return ready, nil
	}
	return c.WaitFor(ctx, fn)
}

// Wait for a specific attribute value in a GroupVersionResource
func (c *TestCluster) WaitForGVR(ctx context.Context, namespace string, gvr schema.GroupVersionResource, gvrName string, expectedValue string, fields ...string) error {
	dynamicClient, err := c.DynamicClient(ctx)
	if err != nil {
		return err
	}
	fn := func(ctx context.Context) (bool, error) {
		g, err := dynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, gvrName, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("GVR not found: %w", err)
		}

		searchedValue, found, err := unstructured.NestedString(g.Object, fields...)
		if err != nil {
			return false, err
		}
		if !found {
			return false, nil
		}

		return searchedValue == expectedValue, nil
	}
	return c.WaitFor(ctx, fn)
}

func (c *TestCluster) WaitForEvent(ctx context.Context, namespace string, gvrName string, resourceKind string, expectedReason string, containedMessage string) error {
	fn := func(ctx context.Context) (bool, error) {
		events, err := c.client.CoreV1().Events(namespace).List(ctx,
			metav1.ListOptions{FieldSelector: fmt.Sprintf("involvedObject.name=%s", gvrName),
				TypeMeta: metav1.TypeMeta{Kind: resourceKind}})
		if err != nil {
			return false, err
		}

		for _, event := range events.Items {
			if event.Reason == expectedReason && strings.Contains(event.Message, containedMessage) {
				return true, nil
			}
		}

		return false, nil
	}
	return c.WaitFor(ctx, fn)
}

// Wait for a specific attribute value in a GroupVersionResource
func (c *TestCluster) WaitForGVRBool(ctx context.Context, namespace string, gvr schema.GroupVersionResource, gvrName string, expectedValue bool, fields ...string) error {
	dynamicClient, err := c.DynamicClient(ctx)
	if err != nil {
		return err
	}
	fn := func(ctx context.Context) (bool, error) {
		g, err := dynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, gvrName, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("GVR not found: %w", err)
		}

		searchedValue, found, err := unstructured.NestedBool(g.Object, fields...)
		if err != nil {
			return false, err
		}
		if !found {
			return false, nil
		}

		return searchedValue == expectedValue, nil
	}
	return c.WaitFor(ctx, fn)
}

// Wait for a specific attribute value in a GroupVersionResource
func (c *TestCluster) WaitForGVRDeletion(ctx context.Context, namespace string, gvr schema.GroupVersionResource, gvrName string) error {
	dynamicClient, err := c.DynamicClient(ctx)
	if err != nil {
		return err
	}
	fn := func(ctx context.Context) (bool, error) {
		_, err := dynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, gvrName, metav1.GetOptions{})
		if err != nil {
			return true, nil
		}

		return false, nil
	}
	return c.WaitFor(ctx, fn)
}
