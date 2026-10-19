package sk8s

// AWS EKS support
//
// LoadConfig, EKSClient, RESTConfig, KubeConfig, GenerateToken, and EKSClusterProvider (see
// eks_token.go, eks_kubeconfig.go, eks_cluster_provider.go) let GetClusterWithProvider point
// sk8s's cluster-agnostic helpers (WaitFor*, HelmInstall, ExecPod, ...) at a real, already-running
// AWS EKS cluster instead of a local k3s container. sk8s never creates or manages the lifecycle of
// an EKS cluster itself -- only Kubernetes/EKS-API access, so bring your own cluster.
//
// Credentials and region: LoadConfig wraps the AWS SDK for Go v2's default chain (same order as
// the AWS CLI): environment variables, shared config/credentials files (including AWS_PROFILE and
// SSO logins), web identity / IRSA, container credentials, then EC2 instance profile. Region comes
// from AWS_REGION / AWS_DEFAULT_REGION, shared config, or an explicit LoadConfigOptions override
// (e.g. config.WithRegion(...)) passed to LoadConfig. See
// https://docs.aws.amazon.com/sdk-for-go/ for exact, version-specific behavior.
//
// IAM: grant least privilege. EKSClusterProvider needs eks:DescribeCluster for the target cluster
// plus whatever RBAC the caller's IAM principal is mapped to inside the cluster (aws-auth
// ConfigMap or EKS access entries). ListClusters (used only by NewEKSClient callers directly, not
// by EKSClusterProvider) needs eks:ListClusters.

import (
	"context"
	"fmt"
	"slices"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/eks/types"
)

// LoadConfig returns the standard AWS SDK v2 configuration for this process.
//
// It uses the default credential chain (same order as the AWS CLI and most official tooling):
// environment variables (AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_SESSION_TOKEN), shared
// config/credentials files, web identity (IRSA / OIDC), container/ECS credentials, then EC2
// instance profile / IMDS. Region resolution follows AWS_REGION / AWS_DEFAULT_REGION, config
// files, then callers can override with LoadConfigOptions.
func LoadConfig(ctx context.Context, opts ...func(*awscfg.LoadOptions) error) (aws.Config, error) {
	return awscfg.LoadDefaultConfig(ctx, opts...)
}

// EKSClient wraps the AWS EKS API with a small, opinionated surface for sk8s.
type EKSClient struct {
	api *eks.Client
}

// NewEKSClient builds an EKS API client from an SDK config (from LoadConfig or a custom aws.Config).
func NewEKSClient(cfg aws.Config) *EKSClient {
	return &EKSClient{api: eks.NewFromConfig(cfg)}
}

// ClusterSummary is a minimal, stable view of an EKS cluster for callers that do not need the full API shape.
type ClusterSummary struct {
	Name       string
	ARN        string
	Status     types.ClusterStatus
	Version    string
	Endpoint   string
	RegionHint string

	// CertificateAuthorityData is the base64-encoded PEM certificate authority data for the
	// cluster's API server, exactly as returned by the EKS API. Used by RESTConfig/KubeConfig
	// (see eks_kubeconfig.go) to build a TLS-trusted client without shelling out to the AWS CLI.
	CertificateAuthorityData string
}

// DescribeCluster returns core cluster metadata. It exercises the credential chain
// and validates that the principal can call eks:DescribeCluster.
func (c *EKSClient) DescribeCluster(ctx context.Context, name string) (*ClusterSummary, error) {
	out, err := c.api.DescribeCluster(ctx, &eks.DescribeClusterInput{Name: aws.String(name)})
	if err != nil {
		return nil, fmt.Errorf("describe cluster %q: %w", name, err)
	}

	cl := out.Cluster
	if cl == nil {
		return nil, fmt.Errorf("describe cluster %q: empty response", name)
	}

	var caData string
	if cl.CertificateAuthority != nil {
		caData = aws.ToString(cl.CertificateAuthority.Data)
	}

	return &ClusterSummary{
		Name:                     aws.ToString(cl.Name),
		ARN:                      aws.ToString(cl.Arn),
		Status:                   cl.Status,
		Version:                  aws.ToString(cl.Version),
		Endpoint:                 aws.ToString(cl.Endpoint),
		RegionHint:               c.api.Options().Region,
		CertificateAuthorityData: caData,
	}, nil
}

// ListClusters returns all cluster names in the client's configured region (sorted).
// It follows EKS ListClusters pagination until complete.
func (c *EKSClient) ListClusters(ctx context.Context) ([]string, error) {
	var names []string
	var next *string
	for {
		out, err := c.api.ListClusters(ctx, &eks.ListClustersInput{NextToken: next})
		if err != nil {
			return nil, fmt.Errorf("list clusters: %w", err)
		}
		names = append(names, out.Clusters...)
		if out.NextToken == nil || aws.ToString(out.NextToken) == "" {
			break
		}
		next = out.NextToken
	}
	slices.Sort(names)
	return names, nil
}
