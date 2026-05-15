package sk8s

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/distribution/reference"
	ociv1 "github.com/opencontainers/image-spec/specs-go/v1"
	tc "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/k3s"
	"helm.sh/helm/v4/pkg/cli"
	corev1 "k8s.io/api/core/v1"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const defaultImage = "rancher/k3s:v1.34.1-k3s1"

var DefaultConfig = ClusterConfig{
	APIServerArg: []string{
		"feature-gates=ImageVolume=true",
	},
	Disable: []string{
		"metrics-server",
	},
}

type LogOptions struct {
	clusterWarnings bool
	podLogs         bool
}

type ClusterOptions struct {
	image       string
	config      ClusterConfig
	customizers []tc.ContainerCustomizer
	logging     *LogOptions
}

type CustomizeClusterOption func(opts *ClusterOptions) error

func (opt CustomizeClusterOption) Customize(opts *ClusterOptions) error {
	return opt(opts)
}

func WithClusterConfig(config ClusterConfig) CustomizeClusterOption {
	return func(opts *ClusterOptions) error {
		opts.config = config

		return nil
	}
}

func WithCustomizers(customize ...tc.ContainerCustomizer) CustomizeClusterOption {
	return func(opts *ClusterOptions) error {
		opts.customizers = customize

		return nil
	}
}

func WithLoggingOptions(clusterWarnings bool, podLogs bool) CustomizeClusterOption {
	return func(opts *ClusterOptions) error {
		opts.logging = &LogOptions{
			clusterWarnings: clusterWarnings,
			podLogs:         podLogs,
		}

		return nil
	}
}

type TestCluster struct {
	cluster       *k3s.K3sContainer
	client        *kubernetes.Clientset
	dynamicClient dynamic.Interface
	apiExtClient  *apiextensionsclientset.Clientset
	provider      *tc.DockerProvider
	helmSettings  *cli.EnvSettings
	tmpDir        string
}

func GetCluster(t *testing.T, ctx context.Context, opts ...CustomizeClusterOption) (*TestCluster, error) {
	options := ClusterOptions{
		image:  defaultImage,
		config: DefaultConfig,
	}

	for _, o := range opts {
		err := o.Customize(&options)
		if err != nil {
			return nil, err
		}
	}

	mConfig, err := options.config.Marshall()
	if err != nil {
		return nil, err
	}
	customize := []tc.ContainerCustomizer{
		tc.WithFiles(
			tc.ContainerFile{
				Reader:            bytes.NewReader(mConfig),
				ContainerFilePath: "/etc/rancher/k3s/config.yaml",
			},
		),
		tc.WithExposedPorts(
			"2345:2345",   // delve debugger
			"32345:32345", // delve debugger
			"9229:9229",   // node debugger
		),
	}

	customize = append(customize, options.customizers...)

	provider, err := tc.NewDockerProvider()
	if err != nil {
		return nil, fmt.Errorf("docker provider: %w", err)
	}

	k3sContainer, err := k3s.Run(
		ctx, options.image,
		customize...,
	)
	tc.CleanupContainer(t, k3sContainer)
	if err != nil {
		return nil, err
	}

	restcfg, err := getClusterConfig(ctx, k3sContainer)
	if err != nil {
		return nil, err
	}

	k8s, err := kubernetes.NewForConfig(restcfg)
	if err != nil {
		return nil, err
	}

	if options.logging != nil && options.logging.clusterWarnings {
		// Background goroutine to log cluster warnings
		go logClusterWarnings(ctx, k8s)
	}

	if options.logging != nil && options.logging.podLogs {
		// Background goroutine to watch and log all pod logs
		go watchAndLogPods(ctx, k8s)
	}

	tmpDir := t.TempDir()

	return &TestCluster{
		cluster:  k3sContainer,
		client:   k8s,
		provider: provider,
		tmpDir:   tmpDir,
	}, nil
}

func (c *TestCluster) Cluster() *k3s.K3sContainer {
	return c.cluster
}

func (c *TestCluster) Client() *kubernetes.Clientset {
	return c.client
}

func (c *TestCluster) DynamicClient(ctx context.Context) (dynamic.Interface, error) {
	if c.dynamicClient == nil {
		config, err := c.getClusterConfig(ctx)
		if err != nil {
			return nil, err
		}
		dynamicClient, err := dynamic.NewForConfig(config)
		if err != nil {
			return nil, err
		}

		c.dynamicClient = dynamicClient
	}

	return c.dynamicClient, nil
}

func (c *TestCluster) ApiExtClient(ctx context.Context) (*apiextensionsclientset.Clientset, error) {
	if c.apiExtClient == nil {
		config, err := c.getClusterConfig(ctx)
		if err != nil {
			return nil, err
		}

		apiExtClient, err := apiextensionsclientset.NewForConfig(config)
		if err != nil {
			return nil, err
		}

		c.apiExtClient = apiExtClient
	}

	return c.apiExtClient, nil
}

// Use for helm chart tests
func (c *TestCluster) LoadImages(ctx context.Context, images ...string) error {
	for _, image := range images {
		ref, err := reference.ParseAnyReference(image)
		if err != nil {
			return err
		}

		digested, ok := ref.(reference.Canonical)
		if ok {
			// save image
			imagesTar, err := os.CreateTemp(os.TempDir(), "images*.tar")
			if err != nil {
				return fmt.Errorf("creating temporary images file %w", err)
			}
			defer func() {
				_ = os.Remove(imagesTar.Name())
			}()

			err = c.provider.SaveImagesWithOpts(ctx, imagesTar.Name(), []string{image})
			if err != nil {
				return fmt.Errorf("saving images %w", err)
			}

			containerPath := "/tmp/" + filepath.Base(imagesTar.Name())
			err = c.cluster.CopyFileToContainer(ctx, imagesTar.Name(), containerPath, 0x644)
			if err != nil {
				return fmt.Errorf("copying image to container %w", err)
			}

			cmd := []string{"ctr", "-n=k8s.io", "images", "import", "--digests", "--base-name", digested.Name(), containerPath}

			exit, reader, err := c.cluster.Exec(ctx, cmd)
			if err != nil {
				return fmt.Errorf("importing image %w", err)
			}
			if exit != 0 {
				b, _ := io.ReadAll(reader)
				return fmt.Errorf("importing image %s", string(b))
			}
		} else {
			err := c.cluster.LoadImages(ctx, image)
			if err != nil {
				return fmt.Errorf("importing image %w", err)
			}
		}
	}

	// Remove temp image archives
	_, _, _ = c.cluster.Exec(ctx, []string{"sh", "-c", "rm /tmp/images*.tar"})

	return nil
}

func (c *TestCluster) LoadImagesWithPlatform(ctx context.Context, images []string, platform *ociv1.Platform) error {
	imagesTar, err := os.CreateTemp(os.TempDir(), "images*.tar")
	if err != nil {
		return fmt.Errorf("creating temporary images file %w", err)
	}
	defer func() {
		_ = os.Remove(imagesTar.Name())
	}()

	var saveOpts []tc.SaveImageOption
	if platform != nil {
		saveOpts = append(saveOpts, tc.SaveDockerImageWithPlatforms(*platform))
	}

	err = c.provider.SaveImagesWithOpts(ctx, imagesTar.Name(), images, saveOpts...)
	if err != nil {
		return fmt.Errorf("saving images %w", err)
	}

	containerPath := "/tmp/" + filepath.Base(imagesTar.Name())
	err = c.cluster.CopyFileToContainer(ctx, imagesTar.Name(), containerPath, 0x644)
	if err != nil {
		return fmt.Errorf("copying image to container %w", err)
	}

	exit, reader, err := c.cluster.Exec(ctx, []string{"ctr", "-n=k8s.io", "images", "import", "--all-platforms", containerPath})
	if err != nil {
		return fmt.Errorf("importing image %w", err)
	}
	if exit != 0 {
		b, _ := io.ReadAll(reader)
		return fmt.Errorf("importing image %s", string(b))
	}

	return nil
}

func (c *TestCluster) getClusterConfig(ctx context.Context) (*rest.Config, error) {
	return getClusterConfig(ctx, c.cluster)
}

func getClusterConfig(ctx context.Context, cluster *k3s.K3sContainer) (*rest.Config, error) {
	kubeConfigYaml, err := cluster.GetKubeConfig(ctx)
	if err != nil {
		return nil, err
	}

	restcfg, err := clientcmd.RESTConfigFromKubeConfig(kubeConfigYaml)
	if err != nil {
		return nil, err
	}

	return restcfg, nil
}

// logClusterWarnings monitors cluster events and logs warnings
func logClusterWarnings(ctx context.Context, client *kubernetes.Clientset) {
	logger := LoggerFromContext(ctx)

	// Watch for events across all namespaces
	watcher, err := client.CoreV1().Events("").Watch(ctx, metav1.ListOptions{})
	if err != nil {
		logger.Printf("Failed to create event watcher: %v", err)
		return
	}
	defer watcher.Stop()

	logger.Println("Started watching cluster events for warnings")

	for {
		select {
		case <-ctx.Done():
			logger.Println("Stopped watching cluster events")
			return
		case event, ok := <-watcher.ResultChan():
			if !ok {
				logger.Println("Event watcher channel closed, restarting...")
				// Try to restart the watcher
				watcher, err = client.CoreV1().Events("").Watch(ctx, metav1.ListOptions{})
				if err != nil {
					logger.Printf("Failed to restart event watcher: %v", err)
					return
				}
				continue
			}

			if event.Object == nil {
				continue
			}

			k8sEvent, ok := event.Object.(*corev1.Event)
			if !ok {
				continue
			}

			// Log Warning events
			if k8sEvent.Type == corev1.EventTypeWarning {
				logger.Printf("[%s] %s/%s: %s - %s",
					k8sEvent.Type,
					k8sEvent.InvolvedObject.Namespace,
					k8sEvent.InvolvedObject.Name,
					k8sEvent.Reason,
					k8sEvent.Message,
				)
			}
		}
	}
}

// watchAndLogPods watches for new pods in the cluster and streams their logs to the logger.
// This function runs in the background and will continue until the context is cancelled.
// It tracks which pods have already been logged to avoid duplicate log streaming.
func watchAndLogPods(ctx context.Context, client *kubernetes.Clientset) {
	logger := LoggerFromContext(ctx)

	// Track pods we're already logging
	loggedPods := make(map[string]bool)

	// Watch for pods across all namespaces
	watcher, err := client.CoreV1().Pods("").Watch(ctx, metav1.ListOptions{})
	if err != nil {
		logger.Printf("Failed to create pod watcher: %v", err)
		return
	}
	defer watcher.Stop()

	logger.Println("Started watching for new pods and streaming logs")

	for {
		select {
		case <-ctx.Done():
			logger.Println("Stopped watching pods")
			return
		case event, ok := <-watcher.ResultChan():
			if !ok {
				logger.Println("Pod watcher channel closed, restarting...")
				// Try to restart the watcher
				watcher, err = client.CoreV1().Pods("").Watch(ctx, metav1.ListOptions{})
				if err != nil {
					logger.Printf("Failed to restart pod watcher: %v", err)
					return
				}
				continue
			}

			if event.Object == nil {
				continue
			}

			pod, ok := event.Object.(*corev1.Pod)
			if !ok {
				continue
			}

			// Create unique key for this pod
			podKey := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)

			// Only process new pods we haven't seen before
			if loggedPods[podKey] {
				continue
			}

			// Wait for pod to be running before streaming logs
			if pod.Status.Phase == corev1.PodRunning {
				loggedPods[podKey] = true
				logger.Printf("New pod detected: %s", podKey)

				// Stream logs for each container in the pod
				for _, container := range pod.Spec.InitContainers {
					go streamPodContainerLogs(ctx, client, pod.Namespace, pod.Name, container.Name)
				}
				for _, container := range pod.Spec.Containers {
					go streamPodContainerLogs(ctx, client, pod.Namespace, pod.Name, container.Name)
				}
			}
		}
	}
}

// streamPodContainerLogs streams logs from a specific container in a pod
func streamPodContainerLogs(ctx context.Context, client *kubernetes.Clientset, namespace, podName, containerName string) {
	logger := LoggerFromContext(ctx)

	logOptions := &corev1.PodLogOptions{
		Container: containerName,
		Follow:    true,
	}

	req := client.CoreV1().Pods(namespace).GetLogs(podName, logOptions)
	stream, err := req.Stream(ctx)
	if err != nil {
		logger.Printf("Failed to stream logs for %s/%s/%s: %v", namespace, podName, containerName, err)
		return
	}
	// Closing the body is the only reliable way to unblock Scanner.Scan() on a
	// follow=true stream after ctx is cancelled; otherwise the test can hang until
	// the global timeout (e.g. arm64 CI).
	stopCloseOnCancel := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = stream.Close()
		case <-stopCloseOnCancel:
			return
		}
	}()
	defer func() {
		close(stopCloseOnCancel)
		_ = stream.Close()
	}()

	logger.Printf("Started streaming logs for %s/%s/%s", namespace, podName, containerName)

	scanner := bufio.NewScanner(stream)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
			logger.Printf("[%s/%s/%s] %s", namespace, podName, containerName, scanner.Text())
		}
	}

	if err := scanner.Err(); err != nil {
		logger.Printf("Error reading logs for %s/%s/%s: %v", namespace, podName, containerName, err)
	}
}
