//go:build integration

package sk8s

import (
	"context"
	"testing"

	tc "github.com/testcontainers/testcontainers-go"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestIntegration_GetCluster_smoke starts a real k3s container (requires Docker).
// Run: go test -tags=integration ./...
func TestIntegration_GetCluster_smoke(t *testing.T) {
	tc.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	c, err := GetCluster(t, ctx)
	if err != nil {
		t.Fatal(err)
	}

	_, err = c.Client().CoreV1().Namespaces().List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil {
		t.Fatalf("kubernetes client: %v", err)
	}
}
