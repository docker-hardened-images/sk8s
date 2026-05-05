# Sk8s

Docker Hardened Images Sk8s (Skates) is a simple framework for creating automated tests for Kubernetes resources.

## Getting started

Install to your project using:

```
go get github.com/docker-hardened-images/sk8s
```

Add a test:

```golang
package main

import (
	"context"
    "time"

    "github.com/docker-hardened-images/sk8s"
    corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Test(t *testing.T) {
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(5*time.Minute))
	defer cancel()

	cluster, err := sk8s.GetCluster(t, ctx)
	require.NoError(t, err)

    testPod := &corev1.Pod{
        ObjectMeta: metav1.ObjectMeta{
            Name:      "my-test-pod",
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

    _, err = cluster.Client().CoreV1().Pods("default").Create(ctx, testPod, metav1.CreateOptions{})
    require.NoError(t, err, "Failed to create test pod")

    err = cluster.WaitForPod(ctx, namespace, "my-test-pod", "running")
    require.NoError(t, err, "Test pod failed to become ready")
}
```
