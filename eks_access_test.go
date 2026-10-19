//go:build eks

package sk8s

import (
	"context"
	"os"
	"testing"

	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/eks/types"
)

// TestEKSClusterAccessible calls DescribeCluster for EKS_CLUSTER_NAME.
// It checks AWS credentials and IAM (eks:DescribeCluster) without using the Kubernetes API.
//
// Run:
//
//	EKS_CLUSTER_NAME=helm-testing go test -tags=eks . -count=1 -v -run TestEKSClusterAccessible
//
// Region follows the usual SDK chain (AWS_REGION, profile, etc.). To force a region:
//
//	EKS_CLUSTER_REGION=eu-central-1 EKS_CLUSTER_NAME=... go test -tags=eks . -count=1 -v -run TestEKSClusterAccessible
func TestEKSClusterAccessible(t *testing.T) {
	t.Parallel()

	name := os.Getenv("EKS_CLUSTER_NAME")
	if name == "" {
		t.Skip("set EKS_CLUSTER_NAME to run this test (optional Layer-1 EKS check; no kubeconfig required)")
	}

	ctx := context.Background()
	var opts []func(*awscfg.LoadOptions) error
	if r := os.Getenv("EKS_CLUSTER_REGION"); r != "" {
		opts = append(opts, awscfg.WithRegion(r))
	}

	cfg, err := LoadConfig(ctx, opts...)
	if err != nil {
		t.Fatalf("load aws config: %v", err)
	}

	client := NewEKSClient(cfg)
	summary, err := client.DescribeCluster(ctx, name)
	if err != nil {
		t.Fatalf("describe cluster: %v", err)
	}

	if summary.Status != types.ClusterStatusActive {
		t.Fatalf("cluster %q status=%s (want %s)", name, summary.Status, types.ClusterStatusActive)
	}

	t.Logf("cluster %q OK: version=%s endpoint=%s region=%s", summary.Name, summary.Version, summary.Endpoint, summary.RegionHint)
}
