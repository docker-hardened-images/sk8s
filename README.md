# Sk8s

Sk8s (Skates) is a Go testing framework for Kubernetes resources. It spins up an ephemeral cluster ([k3s](https://k3s.io/)) via [Testcontainers](https://testcontainers.com/) and provides a rich API for deploying and asserting on workloads — pods, deployments, Helm charts, CRDs, and more.

## Prerequisites

- Go 1.25+
- Docker (or a compatible container runtime supported by Testcontainers)

## Installation

```
go get github.com/docker-hardened-images/sk8s
```

## Quick start

```go
package mypackage_test

import (
    "context"
    "testing"
    "time"

    sk8s "github.com/docker-hardened-images/sk8s"
    "github.com/stretchr/testify/require"
    corev1 "k8s.io/api/core/v1"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestMyPod(t *testing.T) {
    ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(5*time.Minute))
    defer cancel()

    cluster, err := sk8s.GetCluster(t, ctx)
    require.NoError(t, err)

    pod := &corev1.Pod{
        ObjectMeta: metav1.ObjectMeta{
            Name:      "my-test-pod",
            Namespace: "default",
        },
        Spec: corev1.PodSpec{
            Containers: []corev1.Container{
                {
                    Name:    "test",
                    Image:   "busybox:latest",
                    Command: []string{"sleep", "300"},
                },
            },
            RestartPolicy: corev1.RestartPolicyAlways,
        },
    }

    _, err = cluster.Client().CoreV1().Pods("default").Create(ctx, pod, metav1.CreateOptions{})
    require.NoError(t, err)

    err = cluster.WaitForPod(ctx, "default", "my-test-pod", "running")
    require.NoError(t, err)
}
```

The cluster is automatically cleaned up when the test finishes.

Use `HelmInstall` to deploy a chart and then assert on the resulting workloads.

```go
func TestMyChart(t *testing.T) {
    ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(10*time.Minute))
    defer cancel()

    cluster, err := sk8s.GetCluster(t, ctx)
    require.NoError(t, err)

    // Load the image your chart uses so it is available without a registry pull
    // or if the image is behind a private repository.
    err = cluster.LoadImages(ctx, "my-org/my-app:latest")
    require.NoError(t, err)

    // Install from an OCI registry
    source := sk8s.ChartSourceFromOCI("oci://registry.example.com/charts/my-chart", "1.2.3")

    values := map[string]interface{}{
        "replicaCount": 1,
        "ingress": map[string]interface{}{
            "enabled": true,
        },
    }

    err = cluster.HelmInstall(ctx, "my-release", source,
        sk8s.WithNamespace("my-namespace"),
        sk8s.WithInstallValues(),
    )
    require.NoError(t, err)

    err = cluster.WaitForDeployment(ctx, "my-namespace", "my-release-my-chart")
    require.NoError(t, err)
}
```

## Cluster configuration

`GetCluster` accepts functional options to customise the cluster before it starts.

```go
cluster, err := sk8s.GetCluster(t, ctx,
    sk8s.WithClusterConfig(sk8s.ClusterConfig{
        APIServerArg: []string{"feature-gates=SomeFeature=true"},
        Disable:      []string{"metrics-server"},
    }),
    sk8s.WithLoggingOptions(true, true), // stream cluster warnings and pod logs
)
```

`DefaultConfig` disables `metrics-server` and enables the `ImageVolume` feature gate.

### `ClusterConfig` fields

| Field | Description |
|---|---|
| `APIServerArg` | Extra `kube-apiserver` flags |
| `KubeletArg` | Extra kubelet flags |
| `Disable` | k3s components to disable (e.g. `"metrics-server"`) |
| `FlannelBackEnd` | Flannel backend override |
| `ClusterCIRD` | Pod CIDR override |
| `DisableNetworkPolicy` | Disable network policy enforcement |
| `PauseImage` | Custom pause image |
| `WithCustomizers` passes additional [Testcontainers `ContainerCustomizer`](https://golang.testcontainers.org/) options directly to the k3s container (e.g. extra environment variables or bind mounts). |

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## Security

See [SECURITY.md](SECURITY.md).

## License

See [LICENSE](LICENSE).
