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
	clusterProvider *ClusterProvider
	client          *kubernetes.Clientset
	dynamicClient   dynamic.Interface
	apiExtClient    *apiextensionsclientset.Clientset
	helmSettings    *cli.EnvSettings
	tmpDir          string
}

type ClusterProvider interface {
	getCluster(t *testing.T, ctx context.Context) (*TestCluster, error)
	getKubeConfig(ctx context.Context) ([]byte, error)
	loadImages(ctx context.Context, images ...string) error
	loadImagesWithPlatform(ctx context.Context, images []string, platform *ociv1.Platform) error
	exec(ctx context.Context, cmd []string) (int, io.Reader, error)
	copyFileToCluster(ctx context.Context, hostFilePath string, clusterFilePath string, fileMode int64) error
}

func GetCluster(t *testing.T, ctx context.Context) (*TestCluster, error) {
	return GetClusterWithProvider(t, ctx, &K3sClusterProvider{})
}

func GetClusterWithProvider(t *testing.T, ctx context.Context, provider ClusterProvider) (*TestCluster, error) {
	return provider.getCluster(t, ctx)
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

		apiExtClient := apiextensionsclientset.NewForConfigOrDie(config)
		if err != nil {
			return nil, err
		}

		c.apiExtClient = apiExtClient
	}

	return c.apiExtClient, nil
}

func (c *TestCluster) LoadImages(ctx context.Context, images ...string) error {
	p := *c.clusterProvider
	return p.loadImages(ctx, images...)
}

func (c *TestCluster) LoadImagesWithPlatform(ctx context.Context, images []string, platform *ociv1.Platform) error {
	p := *c.clusterProvider
	return p.loadImagesWithPlatform(ctx, images, platform)
}

func (c *TestCluster) getKubeConfig(ctx context.Context) ([]byte, error) {
	p := *c.clusterProvider
	return p.getKubeConfig(ctx)
}

func (c *TestCluster) getClusterConfig(ctx context.Context) (*rest.Config, error) {
	p := *c.clusterProvider
	kubeConfigYaml, err := p.getKubeConfig(ctx)
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
