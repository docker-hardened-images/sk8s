package sk8s

import (
	"context"
	"io"
	"testing"

	ociv1 "github.com/opencontainers/image-spec/specs-go/v1"
	"helm.sh/helm/v4/pkg/cli"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type TestClusterInterface interface {
	Client() *kubernetes.Clientset
}

type TestCluster struct {
	TestClusterInterface
	clusterProvider ClusterProvider
	client          *kubernetes.Clientset
	dynamicClient   dynamic.Interface
	apiExtClient    *apiextensionsclientset.Clientset
	helmSettings    *cli.EnvSettings
	tmpDir          string
}

type ClusterProvider interface {
	GetCluster(t *testing.T, ctx context.Context) (*TestCluster, error)
	GetKubeConfig(ctx context.Context) ([]byte, error)
	LoadImages(ctx context.Context, images ...string) error
	LoadImagesWithPlatform(ctx context.Context, images []string, platform *ociv1.Platform) error
	Exec(ctx context.Context, cmd []string, opts ...CustomizeExecOption) (int, io.Reader, error)
	CopyFileToCluster(ctx context.Context, hostFilePath string, clusterFilePath string, fileMode int64) error
	CopyFileFromCluster(ctx context.Context, clusterFilePath string) (io.ReadCloser, error)
}

func GetCluster(t *testing.T, ctx context.Context) (*TestCluster, error) {
	return GetClusterWithProvider(t, ctx, &K3sClusterProvider{})
}

func GetClusterWithProvider(t *testing.T, ctx context.Context, provider ClusterProvider) (*TestCluster, error) {
	cluster, err := provider.GetCluster(t, ctx)
	if err != nil {
		return nil, err
	}
	cluster.clusterProvider = provider

	return cluster, nil
}

func (c *TestCluster) Provider() ClusterProvider {
	return c.clusterProvider
}

func (c *TestCluster) Client() *kubernetes.Clientset {
	return c.client
}

func (c *TestCluster) DynamicClient(ctx context.Context) (dynamic.Interface, error) {
	if c.dynamicClient == nil {
		config, err := c.GetClusterConfig(ctx)
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
		config, err := c.GetClusterConfig(ctx)
		if err != nil {
			return nil, err
		}

		apiExtClient := apiextensionsclientset.NewForConfigOrDie(config)
		if err != nil {
			return nil, err
		}

		c.apiExtClient = apiExtClient
	}

	return c.apiExtClient, nil
}

func (c *TestCluster) LoadImages(ctx context.Context, images ...string) error {
	return c.clusterProvider.LoadImages(ctx, images...)
}

func (c *TestCluster) LoadImagesWithPlatform(ctx context.Context, images []string, platform *ociv1.Platform) error {
	return c.clusterProvider.LoadImagesWithPlatform(ctx, images, platform)
}

func (c *TestCluster) Exec(ctx context.Context, cmd []string, opts ...CustomizeExecOption) (int, io.Reader, error) {
	return c.clusterProvider.Exec(ctx, cmd, opts...)
}

func (c *TestCluster) CopyFileToCluster(ctx context.Context, hostFilePath string, clusterFilePath string, fileMode int64) error {
	return c.clusterProvider.CopyFileToCluster(ctx, hostFilePath, clusterFilePath, fileMode)
}

func (c *TestCluster) CopyFileFromCluster(ctx context.Context, clusterFilePath string) (io.ReadCloser, error) {
	return c.clusterProvider.CopyFileFromCluster(ctx, clusterFilePath)
}

func (c *TestCluster) GetKubeConfig(ctx context.Context) ([]byte, error) {
	return c.clusterProvider.GetKubeConfig(ctx)
}

func (c *TestCluster) GetClusterConfig(ctx context.Context) (*rest.Config, error) {
	kubeConfigYaml, err := c.clusterProvider.GetKubeConfig(ctx)
	if err != nil {
		return nil, err
	}
	return getClusterConfig(kubeConfigYaml)
}

func getClusterConfig(kubeConfigYaml []byte) (*rest.Config, error) {
	restcfg, err := clientcmd.RESTConfigFromKubeConfig(kubeConfigYaml)
	if err != nil {
		return nil, err
	}

	return restcfg, nil
}

// ExecOptions holds provider-independent settings for executing a command in the cluster.
type ExecOptions struct {
	multiplexed bool
}

type CustomizeExecOption func(opts *ExecOptions)

func (opt CustomizeExecOption) Customize(opts *ExecOptions) {
	opt(opts)
}

// WithMultiplexed makes the command's stdout and stderr be returned as a single
// stream, with any stream framing headers stripped. Without it, the output is
// not multiplexed.
func WithMultiplexed() CustomizeExecOption {
	return func(opts *ExecOptions) {
		opts.multiplexed = true
	}
}

func newExecOptions(opts ...CustomizeExecOption) *ExecOptions {
	execOpts := &ExecOptions{}
	for _, opt := range opts {
		opt.Customize(execOpts)
	}

	return execOpts
}
