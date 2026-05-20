# API Reference

## Types

### TestCluster

`TestCluster` is the central type representing a running test cluster. All cluster operations are methods on this type.

```go
type TestCluster struct {}
```

### ClusterConfig

Configures the K3s cluster at startup. Marshals to a K3s `config.yaml`.

```go
type ClusterConfig struct {
    APIServerArg         []string // kube-apiserver arguments
    KubeletArg           []string // kubelet arguments
    Disable              []string // K3s components to disable
    FlannelBackEnd       string   // flannel backend
    ClusterCIRD          string   // cluster CIDR
    DisableNetworkPolicy bool     // disable network policy
    PauseImage           string   // custom pause image
}

func (c *ClusterConfig) Marshall() ([]byte, error)
```

**Default:**

```go
var DefaultConfig = ClusterConfig{
    APIServerArg: []string{"feature-gates=ImageVolume=true"},
    Disable:      []string{"metrics-server"},
}
```

### HelmInstallOptions

Options for a Helm chart installation. Use `CustomizeHelmInstallOption` functions to populate.

```go
type HelmInstallOptions struct {
    Namespace       string
    Values          map[string]interface{}
    PostRenderer    *postrenderer.PostRenderer
    PreloadedImages []string
}
```

---

## Cluster Creation

### GetCluster

Creates and starts a cluster for testing. The cluster is automatically terminated when the test ends.

```go
func GetCluster(t *testing.T, ctx context.Context, opts ...CustomizeClusterOption) (*TestCluster, error)
```

**Example:**

```go
cluster, err := sk8s.GetCluster(t, ctx,
    sk8s.WithClusterConfig(sk8s.ClusterConfig{
        Disable: []string{
            "traefik",
        },
    }),
    sk8s.WithLoggingOptions(true, false),
)
```

### Cluster Option Functions

```go
func WithClusterConfig(config ClusterConfig) CustomizeClusterOption
func WithCustomizers(customize ...tc.ContainerCustomizer) CustomizeClusterOption
func WithLoggingOptions(clusterWarnings bool, podLogs bool) CustomizeClusterOption
```

---

## Client Access

```go
func (c *TestCluster) Cluster() *k3s.K3sContainer
func (c *TestCluster) Client() *kubernetes.Clientset
func (c *TestCluster) DynamicClient(ctx context.Context) (dynamic.Interface, error)
func (c *TestCluster) ApiExtClient(ctx context.Context) (*apiextensionsclientset.Clientset, error)
```

---

## YAML Operations

### ApplyYAMLFile

Applies a local YAML file using server-side apply, equivalent to `kubectl apply -f`. The `fieldManager` parameter identifies the caller (typically `"test"`).

```go
func (c *TestCluster) ApplyYAMLFile(ctx context.Context, yamlFile string, fieldManager string, extraSchema ...runtime.SchemeBuilder) error
```

### ApplyYAMLData

Same as `ApplyYAMLFile` but operates on an in-memory byte slice. Handles multi-document YAML.

```go
func (c *TestCluster) ApplyYAMLData(ctx context.Context, yamlData []byte, fieldManager string, extraSchema ...runtime.SchemeBuilder) error
```

### ApplyRemoteYAMLs

Downloads and applies YAMLs from remote URLs via `kubectl apply` inside the cluster container.

```go
func (c *TestCluster) ApplyRemoteYAMLs(ctx context.Context, urls []string) error
```

### ApplyLocalYAMLs

Copies local YAML files into the cluster container and applies them via `kubectl`. Useful for CRDs and custom resources not registered in the Go scheme.

```go
func (c *TestCluster) ApplyLocalYAMLs(ctx context.Context, yamlFiles []string) error
```

---

## Helm Operations

### HelmInstall

Installs a Helm chart into the cluster.

```go
func (c *TestCluster) HelmInstall(ctx context.Context, releaseName string, chartSource chartSource, opts ...CustomizeHelmInstallOption) error
```

### HelmUninstall

```go
func (c *TestCluster) HelmUninstall(ctx context.Context, releaseName string) error
```

### HelmSettings / HelmGetNamespace / HelmSetNamespace

```go
func (c *TestCluster) HelmSettings(ctx context.Context) (*cli.EnvSettings, error)
func (c *TestCluster) HelmGetNamespace(ctx context.Context) (string, error)
func (c *TestCluster) HelmSetNamespace(ctx context.Context, namespace string) error
```

### ConvertTypeToHelmValues

Converts a typed struct to `map[string]interface{}` for use as Helm values.

```go
func ConvertTypeToHelmValues(v interface{}) (map[string]interface{}, error)
```

### Helm Install Option Functions

```go
func WithNamespace(namespace string) CustomizeHelmInstallOption
func WithInstallValues(values map[string]interface{}) CustomizeHelmInstallOption
func WithPostRenderer(postRenderer *postrenderer.PostRenderer) CustomizeHelmInstallOption
func WithPreloadedImages(images ...string) CustomizeHelmInstallOption
```

### Chart Source Functions

Create a `chartSource` value to pass to `HelmInstall`.

```go
// From an OCI registry reference and version
func ChartSourceFromOCI(ref string, version string) chartSource

// From a Helm repository URL (latest version)
func ChartSourceFromUrl(repoUrl string, chartName string) chartSource

// From a Helm repository URL with a specific chart version
func ChartSourceFromUrlAndVersion(repoUrl string, chartName string, chartVersion string) chartSource

// From a Helm repository URL with a specific version and a fallback if not found
func ChartSourceFromUrlAndVersionWithFallback(repoUrl string, chartName string, chartVersion string, fallbackVersion string) chartSource

// From a Helm repository URL, resolving the chart version by its appVersion field
func ChartSourceFromUrlAndAppVersion(repoUrl string, chartName string, appVersion string) chartSource
```

---

## Image Loading

```go
func (c *TestCluster) LoadImages(ctx context.Context, images ...string) error
func (c *TestCluster) LoadImagesWithPlatform(ctx context.Context, images []string, platform *ociv1.Platform) error
```

---

## Execution

### Exec

Executes a command in the K3s container itself.

```go
func (c *TestCluster) Exec(ctx context.Context, cmd []string, options ...exec.ProcessOption) (int, io.Reader, error)
```

### ExecPod

Executes a command in the default container of a pod.

```go
func (c *TestCluster) ExecPod(ctx context.Context, namespace string, pod string, cmd []string) (bytes.Buffer, bytes.Buffer, error)
```

### ExecPodContainer

Executes a command in a specific container of a pod.

```go
func (c *TestCluster) ExecPodContainer(ctx context.Context, namespace string, pod string, container string, cmd []string) (bytes.Buffer, bytes.Buffer, error)
```

### RunJob

Creates a `batch/v1 Job`, waits for it to complete, and returns its log output.

```go
func (c *TestCluster) RunJob(ctx context.Context, namespace string, job *batchv1.Job) (string, error)
```

### GetRaw

Performs a raw GET against the Kubernetes API, equivalent to `kubectl get --raw <apiPath>`.

```go
func (c *TestCluster) GetRaw(ctx context.Context, apiPath string) (string, error)
```

---

## Resource Management

```go
func (c *TestCluster) CreateNamespace(ctx context.Context, namespace string) error
func (c *TestCluster) DeleteResource(ctx context.Context, gvr schema.GroupVersionResource, namespace, name string) error
func (c *TestCluster) ScaleDeployment(ctx context.Context, namespace string, deploymentName string, replicas int32) error
func (c *TestCluster) CreateMockCert(ctx context.Context, namespace string, certName string, caCrt, tlsCert, tlsKey []byte) error
```

### Pod Retrieval

```go
func (c *TestCluster) GetPodsForDeployment(ctx context.Context, namespace string, deploymentName string) ([]corev1.Pod, error)
func (c *TestCluster) GetPodsForStatefulSet(ctx context.Context, namespace string, stsName string) ([]corev1.Pod, error)
func (c *TestCluster) GetPodsForDaemonSet(ctx context.Context, namespace string, dsName string) ([]corev1.Pod, error)
```

---

## Wait Functions

All wait functions poll until the condition is met or the context is cancelled.

### Generic

```go
func (c *TestCluster) WaitFor(ctx context.Context, fn func(ctx context.Context) (bool, error)) error
func (c *TestCluster) WaitForWithTimeout(ctx context.Context, interval time.Duration, timeout time.Duration, immediate bool, condition kwait.ConditionWithContextFunc) error
```

### Pods

```go
// state: "ready" | "running" | "completed" | "terminated"
func (c *TestCluster) WaitForPod(ctx context.Context, namespace string, pod string, state string) error
func (c *TestCluster) WaitForState(ctx context.Context, namespace string, pod string, state string) error // alias for WaitForPod
func (c *TestCluster) WaitForPodByLabel(ctx context.Context, namespace string, labelSelector string) (*corev1.Pod, error)
```

### Workloads

```go
func (c *TestCluster) WaitForDeployment(ctx context.Context, namespace string, deploymentName string) error
func (c *TestCluster) WaitForDeploymentReplicaCount(ctx context.Context, namespace string, deploymentName string, want int32) error
func (c *TestCluster) WaitForStatefulSet(ctx context.Context, namespace string, stsName string) error
func (c *TestCluster) WaitForDaemonSet(ctx context.Context, namespace string, dsName string) error
func (c *TestCluster) WaitForJob(ctx context.Context, namespace string, jobName string) error
```

### Services

```go
// Waits until the service has at least one ready endpoint
func (c *TestCluster) WaitForService(ctx context.Context, namespace string, serviceName string) error

// Waits until a LoadBalancer service has an external IP assigned
func (c *TestCluster) WaitForExternalService(ctx context.Context, namespace string, serviceName string) error
```

### Storage

```go
func (c *TestCluster) WaitForPVC(ctx context.Context, namespace string, pvcName string) error
func (c *TestCluster) WaitForSecret(ctx context.Context, namespace string, secretName string) error
```

### API / CRDs

```go
func (c *TestCluster) WaitForCRD(ctx context.Context, crdName string) error
func (c *TestCluster) WaitForAPIResource(ctx context.Context, apiGroup string, resourceName string, kind string) error
```

### Logs

```go
func (c *TestCluster) WaitForLog(ctx context.Context, namespace string, pod string, log string) error
func (c *TestCluster) WaitForLogWithOptions(ctx context.Context, namespace string, pod string, log string, options *corev1.PodLogOptions) error

// Waits for a log line that contains all elements of the log slice
func (c *TestCluster) WaitForLogLineWithOptions(ctx context.Context, namespace string, pod string, log []string, options *corev1.PodLogOptions) error
```

### Events

```go
func (c *TestCluster) WaitForEvent(ctx context.Context, namespace string, gvrName string, resourceKind string, expectedReason string, containedMessage string) error
```

### Generic Resources (GVR)

```go
// Waits for a string field to equal expectedValue
func (c *TestCluster) WaitForGVR(ctx context.Context, namespace string, gvr schema.GroupVersionResource, gvrName string, expectedValue string, fields ...string) error

// Waits for a bool field to equal expectedValue
func (c *TestCluster) WaitForGVRBool(ctx context.Context, namespace string, gvr schema.GroupVersionResource, gvrName string, expectedValue bool, fields ...string) error

// Waits for the resource to be deleted
func (c *TestCluster) WaitForGVRDeletion(ctx context.Context, namespace string, gvr schema.GroupVersionResource, gvrName string) error
```

---

## Debugging

### SetupGoDebug

Loads the Delve debugger image into the cluster.

```go
func (c *TestCluster) SetupGoDebug(t *testing.T, delveVersion string) error
```

### DebugGoDeployment

Modifies a deployment to run under Delve, exposing port `2345` via a LoadBalancer service.

```go
func (c *TestCluster) DebugGoDeployment(t *testing.T, namespace string, deploymentName string, cmd string) error
```

### DumpPodsStatus

Logs detailed status (phase, containers, events) for a list of pods.

```go
func (c *TestCluster) DumpPodsStatus(ctx context.Context, pods []corev1.Pod)
```

---

## TLS Configuration

Builds a `*tls.Config` for use with Helm registry clients.

```go
func NewTLSConfig(options ...TLSConfigOption) (*tls.Config, error)

func WithInsecureSkipVerify(insecureSkipTLSverify bool) TLSConfigOption
func WithCertKeyPairFiles(certFile, keyFile string) TLSConfigOption
func WithCAFile(caFile string) TLSConfigOption
```
