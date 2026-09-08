package sk8s

import (
	"context"
	"fmt"
	"io"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	ociv1 "github.com/opencontainers/image-spec/specs-go/v1"
	"k8s.io/client-go/kubernetes"
)

// EKSClusterProvider is a ClusterProvider that points sk8s's cluster-agnostic helpers (WaitFor*,
// HelmInstall, ExecPod, ...) at an already-running AWS EKS cluster, instead of creating a new one
// (see GetCluster for that). Use it with GetClusterWithProvider:
//
//	cfg, err := sk8s.LoadConfig(ctx)
//	cluster, err := sk8s.GetClusterWithProvider(t, ctx, &sk8s.EKSClusterProvider{
//		AWSConfig:   cfg,
//		ClusterName: "helm-sandbox-test",
//	})
//
// EKSClusterProvider does not create, wait for, or otherwise manage the cluster: ClusterName
// must already exist and be ACTIVE. Creating a real EKS cluster from scratch commonly takes
// 10-15 minutes, so tests using this should target a pre-existing, already-warm cluster rather
// than provisioning one per run.
//
// Because there is no local container backing it, LoadImages, LoadImagesWithPlatform, Exec, and
// ApplyRemoteYAMLs/ApplyLocalYAMLs are not supported on a TestCluster built from this provider:
// push images to a registry the cluster can pull from, and use TestCluster's ExecPod or RunJob
// for in-cluster commands instead.
type EKSClusterProvider struct {
	AWSConfig   awssdk.Config
	ClusterName string
}

func (p *EKSClusterProvider) getCluster(t *testing.T, ctx context.Context) (*TestCluster, error) {
	if p.ClusterName == "" {
		return nil, fmt.Errorf("ClusterName is required")
	}

	// Build the typed clientset from a REST config with an auto-refreshing SigV4 transport
	// (rather than the static, ~15-minute token from getKubeConfig), so it doesn't go stale over
	// the life of a longer test run.
	restConfig, err := RESTConfig(ctx, p.AWSConfig, p.ClusterName)
	if err != nil {
		return nil, fmt.Errorf("get REST config for cluster %q: %w", p.ClusterName, err)
	}

	k8s, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("build kubernetes client: %w", err)
	}

	return &TestCluster{
		client:          k8s,
		clusterProvider: p,
		tmpDir:          t.TempDir(),
	}, nil
}

// getKubeConfig returns a portable kubeconfig YAML document with a short-lived (~15 minute)
// static bearer token, for tools (Helm, see TestCluster.HelmSettings) that need a file on disk
// rather than a live *rest.Config. It is fetched fresh on every call (see ClusterProvider), so it
// doesn't go stale mid-test.
func (p *EKSClusterProvider) getKubeConfig(ctx context.Context) ([]byte, error) {
	kubeConfig, _, err := KubeConfig(ctx, p.AWSConfig, p.ClusterName)

	return kubeConfig, err
}

func (p *EKSClusterProvider) loadImages(ctx context.Context, images ...string) error {
	return fmt.Errorf("loadImages is not supported for an existing EKS cluster (no local container); push images to a registry the cluster can pull from instead")
}

func (p *EKSClusterProvider) loadImagesWithPlatform(ctx context.Context, images []string, platform *ociv1.Platform) error {
	return fmt.Errorf("loadImagesWithPlatform is not supported for an existing EKS cluster (no local container); push images to a registry the cluster can pull from instead")
}

func (p *EKSClusterProvider) exec(ctx context.Context, cmd []string) (int, io.Reader, error) {
	return 0, nil, fmt.Errorf("exec is not supported for an existing EKS cluster (no local container); use TestCluster.ExecPod or RunJob instead")
}

func (p *EKSClusterProvider) copyFileToCluster(ctx context.Context, hostFilePath string, clusterFilePath string, fileMode int64) error {
	return fmt.Errorf("copyFileToCluster is not supported for an existing EKS cluster (no local container)")
}
