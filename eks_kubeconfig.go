package sk8s

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// eksTokenRoundTripper injects a freshly generated EKS bearer token on every request. Because
// GenerateToken is a local, network-free SigV4 presign, refreshing on every request is cheap and
// means the returned *rest.Config never suffers the ~15 minute token expiry that a static
// Authorization header would.
type eksTokenRoundTripper struct {
	base        http.RoundTripper
	cfg         aws.Config
	clusterName string
}

func (t *eksTokenRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	token, _, err := GenerateToken(req.Context(), t.cfg, t.clusterName)
	if err != nil {
		return nil, fmt.Errorf("refresh EKS token: %w", err)
	}

	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+token)

	return t.base.RoundTrip(req)
}

// RESTConfig returns a *rest.Config for the named, already-running EKS cluster. It describes the
// cluster to obtain the API server endpoint and certificate authority, then wires authentication
// through a transport that mints a fresh bearer token (see GenerateToken) on every request -- so,
// unlike a kubeconfig with a static embedded token, the returned config remains usable for the
// life of the process instead of expiring after ~15 minutes.
//
// This does not create, wait for, or otherwise manage the cluster: the named cluster must already
// exist and be ACTIVE. Creating a real EKS cluster from scratch commonly takes 10-15 minutes, so
// tests and tools built on this should target a pre-existing, already-warm cluster rather than
// provisioning one per run.
func RESTConfig(ctx context.Context, cfg aws.Config, clusterName string) (*rest.Config, error) {
	client := NewEKSClient(cfg)

	summary, err := client.DescribeCluster(ctx, clusterName)
	if err != nil {
		return nil, fmt.Errorf("describe cluster %q: %w", clusterName, err)
	}

	caData, err := decodeClusterCA(summary.CertificateAuthorityData)
	if err != nil {
		return nil, err
	}

	rc := &rest.Config{
		Host: summary.Endpoint,
		TLSClientConfig: rest.TLSClientConfig{
			CAData: caData,
		},
		WrapTransport: func(rt http.RoundTripper) http.RoundTripper {
			return &eksTokenRoundTripper{base: rt, cfg: cfg, clusterName: clusterName}
		},
	}

	return rc, nil
}

// KubeConfig renders a standalone kubeconfig YAML document for the named, already-running EKS
// cluster, with a short-lived static bearer token embedded (see GenerateToken; valid ~15 minutes
// from generation). Unlike RESTConfig, the returned bytes are a portable, self-contained
// kubeconfig with no auto-refresh -- suitable for EKSClusterProvider's Helm settings, `kubectl
// --kubeconfig=...`, or any tool that expects a kubeconfig file rather than a live *rest.Config.
//
// Callers with test runs that may exceed the token's lifetime should call KubeConfig again close
// to expiration (the returned expiresAt) to mint a fresh document, rather than reusing one past
// its validity window.
func KubeConfig(ctx context.Context, cfg aws.Config, clusterName string) (kubeConfig []byte, expiresAt time.Time, err error) {
	client := NewEKSClient(cfg)

	summary, err := client.DescribeCluster(ctx, clusterName)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("describe cluster %q: %w", clusterName, err)
	}

	caData, err := decodeClusterCA(summary.CertificateAuthorityData)
	if err != nil {
		return nil, time.Time{}, err
	}

	token, expiresAt, err := GenerateToken(ctx, cfg, clusterName)
	if err != nil {
		return nil, time.Time{}, err
	}

	const contextName = "sk8s-eks"

	apiCfg := clientcmdapi.Config{
		Kind:       "Config",
		APIVersion: "v1",
		Clusters: map[string]*clientcmdapi.Cluster{
			contextName: {
				Server:                   summary.Endpoint,
				CertificateAuthorityData: caData,
			},
		},
		Contexts: map[string]*clientcmdapi.Context{
			contextName: {
				Cluster:  contextName,
				AuthInfo: contextName,
			},
		},
		CurrentContext: contextName,
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			contextName: {
				Token: token,
			},
		},
	}

	data, err := clientcmd.Write(apiCfg)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("render kubeconfig: %w", err)
	}

	return data, expiresAt, nil
}

func decodeClusterCA(base64CA string) ([]byte, error) {
	if base64CA == "" {
		return nil, fmt.Errorf("cluster has no certificate authority data (is it still creating?)")
	}

	caData, err := base64.StdEncoding.DecodeString(base64CA)
	if err != nil {
		return nil, fmt.Errorf("decode cluster certificate authority data: %w", err)
	}

	return caData, nil
}
