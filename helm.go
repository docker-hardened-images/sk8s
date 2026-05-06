package sk8s

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart"
	"helm.sh/helm/v4/pkg/chart/loader"
	"helm.sh/helm/v4/pkg/cli"
	"helm.sh/helm/v4/pkg/downloader"
	"helm.sh/helm/v4/pkg/getter"
	"helm.sh/helm/v4/pkg/postrenderer"
	"helm.sh/helm/v4/pkg/registry"
	repo "helm.sh/helm/v4/pkg/repo/v1"
)

var helmDriver string = os.Getenv("HELM_DRIVER")

type chartSource interface {
	GetRef() (string, string, error)
	GetChartVersion() string
}

type chartSourceAbsoluteRef struct {
	ref     string
	version string
}

func (s *chartSourceAbsoluteRef) GetRef() (string, string, error) {
	return s.ref, s.version, nil
}

func (s *chartSourceAbsoluteRef) GetChartVersion() string {
	return s.version
}

func ChartSourceFromOCI(ref string, version string) chartSource {
	return &chartSourceAbsoluteRef{
		ref:     ref,
		version: version,
	}
}

type chartSourceFromUrlAndVersion struct {
	repoUrl         string
	chartName       string
	chartVersion    string
	appVersion      string
	fallbackVersion *string
}

func (s *chartSourceFromUrlAndVersion) GetRef() (string, string, error) {
	version := s.chartVersion
	if s.appVersion != "" && s.chartVersion == "" {
		chartVersion, err := searchChartByAppVersion(s.repoUrl, s.appVersion, s.chartName)
		if err != nil {
			return "", "", err
		}

		if chartVersion == "" {
			return "", "", fmt.Errorf("no chart found for version %s", chartVersion)
		}

		version = chartVersion
	}

	ref, err := repo.FindChartInRepoURL(s.repoUrl, s.chartName, getter.All(&cli.EnvSettings{}), repo.WithChartVersion(version))
	if err != nil {
		if s.chartVersion != "" && s.fallbackVersion != nil {
			version = ""
			ref, err = repo.FindChartInRepoURL(s.repoUrl, s.chartName, getter.All(&cli.EnvSettings{}))
			if err != nil {
				return "", "", err
			}
		} else {
			return "", "", err
		}
	}

	return ref, version, nil
}

func ChartSourceFromUrl(repoUrl string, chartName string) chartSource {
	return &chartSourceFromUrlAndVersion{
		repoUrl:      repoUrl,
		chartName:    chartName,
		chartVersion: "",
	}
}

func (s *chartSourceFromUrlAndVersion) GetChartVersion() string {
	return s.chartVersion
}

func ChartSourceFromUrlAndVersion(repoUrl string, chartName string, chartVersion string) chartSource {
	return &chartSourceFromUrlAndVersion{
		repoUrl:      repoUrl,
		chartName:    chartName,
		chartVersion: chartVersion,
	}
}

func ChartSourceFromUrlAndVersionWithFallback(repoUrl string, chartName string, chartVersion string, fallbackVersion string) chartSource {
	return &chartSourceFromUrlAndVersion{
		repoUrl:         repoUrl,
		chartName:       chartName,
		chartVersion:    chartVersion,
		fallbackVersion: &fallbackVersion,
	}
}

func ChartSourceFromUrlAndAppVersion(repoUrl string, chartName string, appVersion string) chartSource {
	return &chartSourceFromUrlAndVersion{
		repoUrl:    repoUrl,
		chartName:  chartName,
		appVersion: appVersion,
	}
}

type HelmInstallOptions struct {
	Namespace       string
	Values          map[string]interface{}
	PostRenderer    *postrenderer.PostRenderer
	PreloadedImages []string
}

type CustomizeHelmInstallOption func(opts *HelmInstallOptions) error

func (opt CustomizeHelmInstallOption) Customize(opts *HelmInstallOptions) error {
	return opt(opts)
}

func WithNamespace(namespace string) CustomizeHelmInstallOption {
	return func(opts *HelmInstallOptions) error {
		opts.Namespace = namespace

		return nil
	}
}

func WithInstallValues(values map[string]interface{}) CustomizeHelmInstallOption {
	return func(opts *HelmInstallOptions) error {
		opts.Values = values

		return nil
	}
}

func WithPostRenderer(postRenderer *postrenderer.PostRenderer) CustomizeHelmInstallOption {
	return func(opts *HelmInstallOptions) error {
		opts.PostRenderer = postRenderer

		return nil
	}
}

func WithPreloadedImages(images ...string) CustomizeHelmInstallOption {
	return func(opts *HelmInstallOptions) error {
		opts.PreloadedImages = images

		return nil
	}
}

func (c *TestCluster) HelmSettings(ctx context.Context) (*cli.EnvSettings, error) {
	if c.helmSettings == nil {
		path := filepath.Join(c.tmpDir, "helm")

		err := os.Mkdir(path, 0755)
		if err != nil {
			return nil, err
		}

		kubeConfigFile := filepath.Join(path, "kubeconfig")
		registryConfigFile := filepath.Join(path, "registry_config.json")
		repoConfigFile := filepath.Join(path, "repositories.yaml")
		repoCacheDir := filepath.Join(path, "repository")
		pluginsDir := filepath.Join(path, "plugins")

		kubeConfig, err := c.cluster.GetKubeConfig(ctx)
		if err != nil {
			return nil, err
		}

		err = os.WriteFile(kubeConfigFile, kubeConfig, 0644)
		if err != nil {
			return nil, err
		}

		settings := cli.New()

		settings.KubeConfig = kubeConfigFile
		settings.RegistryConfig = registryConfigFile
		settings.RepositoryConfig = repoConfigFile
		settings.RepositoryCache = repoCacheDir
		settings.PluginsDirectory = pluginsDir

		settings.SetNamespace("default")

		c.helmSettings = settings
	}

	return c.helmSettings, nil
}

func (c *TestCluster) HelmGetNamespace(ctx context.Context) (string, error) {
	settings, err := c.HelmSettings(ctx)
	if err != nil {
		return "", err
	}

	return settings.Namespace(), nil
}

func (c *TestCluster) HelmSetNamespace(ctx context.Context, namespace string) error {
	settings, err := c.HelmSettings(ctx)
	if err != nil {
		return err
	}
	settings.SetNamespace(namespace)
	return nil
}

func (c *TestCluster) HelmInstall(ctx context.Context, releaseName string, chartSource chartSource, opts ...CustomizeHelmInstallOption) error {
	options := HelmInstallOptions{}

	chartRef, chartVersion, err := chartSource.GetRef()
	if err != nil {
		return err
	}

	for _, o := range opts {
		err := o.Customize(&options)
		if err != nil {
			return err
		}
	}

	if len(options.PreloadedImages) > 0 {
		err = c.LoadImages(ctx, options.PreloadedImages...)
		if err != nil {
			return err
		}
	}

	settings, err := c.HelmSettings(ctx)
	if err != nil {
		return err
	}

	logger := log.Default()

	// If no namespace is set but a previous install changed the namespace
	// revert to the default namespace
	if options.Namespace == "" && settings.Namespace() != "default" {
		err = c.HelmSetNamespace(ctx, "default")
		if err != nil {
			return err
		}
	}

	if options.Namespace != "" && options.Namespace != "default" {
		err = c.CreateNamespace(ctx, options.Namespace)
		if err != nil {
			return err
		}

		err = c.HelmSetNamespace(ctx, options.Namespace)
		if err != nil {
			return err
		}
	}

	return helmInstall(ctx, logger, settings, releaseName, chartRef, chartVersion, options.Values, options.PostRenderer)
}

// searchChartByAppVersion fetches the helm chart to apply from the given repoUrl, matching the chart's version by
// appVersion (required) and name (optional). When not found, returns the latest chart.
func searchChartByAppVersion(repoURL, appVersion string, name string) (string, error) {
	getters := getter.All(&cli.EnvSettings{})
	chartRepo, err := repo.NewChartRepository(&repo.Entry{
		URL: repoURL,
	}, getters)
	if err != nil {
		return "", fmt.Errorf("failed to create chart repository: %w", err)
	}

	indexFile, err := chartRepo.DownloadIndexFile()
	if err != nil {
		return "", fmt.Errorf("failed to download index file: %w", err)
	}

	index, err := repo.LoadIndexFile(indexFile)
	if err != nil {
		return "", fmt.Errorf("failed to load index file: %w", err)
	}

	var mostRecentChart string
	var mostRecentTime time.Time

	for _, versions := range index.Entries {
		for _, cv := range versions {
			if cv.Name == name {
				// Check if this is an exact match
				if cv.AppVersion == appVersion {
					return cv.Version, nil
				}

				// Track the most recent chart
				if cv.Created.After(mostRecentTime) {
					mostRecentTime = cv.Created
					mostRecentChart = cv.Version
				}
			}
		}
	}

	return mostRecentChart, nil
}

func (c *TestCluster) HelmUninstall(ctx context.Context, releaseName string) error {
	settings, err := c.HelmSettings(ctx)
	if err != nil {
		return err
	}

	logger := log.Default()

	actionConfig, err := initActionConfig(settings, logger)
	if err != nil {
		return fmt.Errorf("failed to init action config: %w", err)
	}

	uninstallClient := action.NewUninstall(actionConfig)
	uninstallClient.WaitStrategy = "hookOnly"

	_, err = uninstallClient.Run(releaseName)
	if err != nil {
		return fmt.Errorf("failed to run uninstall: %w", err)
	}

	logger.Printf("release uninstalled")
	return nil
}

// ConvertTypeToHelmValues converts a typed struct to map[string]interface{} for Helm
func ConvertTypeToHelmValues(v interface{}) (map[string]interface{}, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	err = json.Unmarshal(data, &result)
	return result, err
}

// Source: https://helm.sh/docs/sdk/examples/#driver
func helmInstall(ctx context.Context, logger *log.Logger, settings *cli.EnvSettings, releaseName string, chartRef string, chartVersion string, releaseValues map[string]interface{}, postRenderer *postrenderer.PostRenderer) error {
	actionConfig, err := initActionConfig(settings, logger)
	if err != nil {
		return fmt.Errorf("failed to init action config: %w", err)
	}

	installClient := action.NewInstall(actionConfig)

	installClient.DryRunStrategy = "none"
	installClient.WaitStrategy = "hookOnly"
	// Match Helm CLI default (pkg/cmd/install.go). Timeout 0 becomes kube.DefaultStatusWatcherTimeout (30s),
	// which is too short for some initializations ref: https://github.com/helm/helm/blob/main/pkg/cmd/install.go#L195
	installClient.Timeout = 300 * time.Second
	installClient.ReleaseName = releaseName
	installClient.Namespace = settings.Namespace()
	installClient.Version = chartVersion
	installClient.ChartPathOptions.Version = chartVersion

	if postRenderer != nil {
		installClient.PostRenderer = *postRenderer
	}

	registryClient, err := newRegistryClient(
		settings,
		installClient.CertFile,
		installClient.KeyFile,
		installClient.CaFile,
		installClient.InsecureSkipTLSVerify,
		installClient.PlainHTTP)
	if err != nil {
		return fmt.Errorf("failed to created registry client: %w", err)
	}
	installClient.SetRegistryClient(registryClient)

	chartPath, err := installClient.ChartPathOptions.LocateChart(chartRef, settings)
	if err != nil {
		return err
	}

	providers := getter.All(settings)

	charter, err := loader.Load(chartPath)
	if err != nil {
		return err
	}

	chartAccessor, err := chart.NewDefaultAccessor(charter)
	if err != nil {
		return fmt.Errorf("error creating chart accessor: %w", err)
	}

	// Check chart dependencies to make sure all are present in /charts
	if chartDependencies := chartAccessor.MetaDependencies(); chartDependencies != nil {
		if err := action.CheckDependencies(charter, chartDependencies); err != nil {
			err = fmt.Errorf("failed to check chart dependencies: %w", err)
			if !installClient.DependencyUpdate {
				return err
			}

			manager := &downloader.Manager{
				Out:              logger.Writer(),
				ChartPath:        chartPath,
				Keyring:          installClient.ChartPathOptions.Keyring,
				SkipUpdate:       false,
				Getters:          providers,
				RepositoryConfig: settings.RepositoryConfig,
				RepositoryCache:  settings.RepositoryCache,
				Debug:            settings.Debug,
				RegistryClient:   installClient.GetRegistryClient(),
			}
			if err := manager.Update(); err != nil {
				return err
			}
			// Reload the chart with the updated Chart.lock file.
			if charter, err = loader.Load(chartPath); err != nil {
				return fmt.Errorf("failed to reload chart after repo update: %w", err)
			}
		}
	}

	_, err = installClient.RunWithContext(ctx, charter, releaseValues)
	if err != nil {
		return fmt.Errorf("failed to run install: %w", err)
	}

	logger.Printf("release created")

	return nil
}

func initActionConfig(settings *cli.EnvSettings, logger *log.Logger) (*action.Configuration, error) {
	return initActionConfigList(settings, logger, false)
}

func initActionConfigList(settings *cli.EnvSettings, logger *log.Logger, allNamespaces bool) (*action.Configuration, error) {

	actionConfig := new(action.Configuration)

	namespace := func() string {
		// For list action, you can pass an empty string instead of settings.Namespace() to list
		// all namespaces
		if allNamespaces {
			return ""
		}
		return settings.Namespace()
	}()

	if err := actionConfig.Init(
		settings.RESTClientGetter(),
		namespace,
		helmDriver); err != nil {
		return nil, err
	}

	return actionConfig, nil
}

func newRegistryClient(settings *cli.EnvSettings, certFile, keyFile, caFile string, insecureSkipTLSVerify, plainHTTP bool) (*registry.Client, error) {

	opts := []registry.ClientOption{
		registry.ClientOptDebug(settings.Debug),
		registry.ClientOptEnableCache(true),
		registry.ClientOptWriter(os.Stderr),
		registry.ClientOptCredentialsFile(settings.RegistryConfig),
	}

	if plainHTTP {
		opts = append(opts, registry.ClientOptPlainHTTP())
	}

	if certFile != "" && keyFile != "" || caFile != "" || insecureSkipTLSVerify {
		tlsConf, err := NewTLSConfig(
			WithInsecureSkipVerify(insecureSkipTLSVerify),
			WithCertKeyPairFiles(certFile, keyFile),
			WithCAFile(caFile),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to load client TLS certs: %w", err)
		}

		opts = append(opts, registry.ClientOptHTTPClient(&http.Client{
			Transport: &http.Transport{
				TLSClientConfig: tlsConf,
				Proxy:           http.ProxyFromEnvironment,
			},
		}))
	}

	// Create a new registry client
	registryClient, err := registry.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize registry client: %w", err)
	}

	return registryClient, nil
}

type TLSConfigOptions struct {
	insecureSkipTLSverify     bool
	certPEMBlock, keyPEMBlock []byte
	caPEMBlock                []byte
}

type TLSConfigOption func(options *TLSConfigOptions) error

func WithInsecureSkipVerify(insecureSkipTLSverify bool) TLSConfigOption {
	return func(options *TLSConfigOptions) error {
		options.insecureSkipTLSverify = insecureSkipTLSverify

		return nil
	}
}

func WithCertKeyPairFiles(certFile, keyFile string) TLSConfigOption {
	return func(options *TLSConfigOptions) error {
		if certFile == "" && keyFile == "" {
			return nil
		}

		certPEMBlock, err := os.ReadFile(certFile)
		if err != nil {
			return fmt.Errorf("unable to read cert file: %q: %w", certFile, err)
		}

		keyPEMBlock, err := os.ReadFile(keyFile)
		if err != nil {
			return fmt.Errorf("unable to read key file: %q: %w", keyFile, err)
		}

		options.certPEMBlock = certPEMBlock
		options.keyPEMBlock = keyPEMBlock

		return nil
	}
}

func WithCAFile(caFile string) TLSConfigOption {
	return func(options *TLSConfigOptions) error {
		if caFile == "" {
			return nil
		}

		caPEMBlock, err := os.ReadFile(caFile)
		if err != nil {
			return fmt.Errorf("can't read CA file: %q: %w", caFile, err)
		}

		options.caPEMBlock = caPEMBlock

		return nil
	}
}

func NewTLSConfig(options ...TLSConfigOption) (*tls.Config, error) {
	to := TLSConfigOptions{}

	errs := []error{}
	for _, option := range options {
		err := option(&to)
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	config := tls.Config{
		InsecureSkipVerify: to.insecureSkipTLSverify,
	}

	if len(to.certPEMBlock) > 0 && len(to.keyPEMBlock) > 0 {
		cert, err := tls.X509KeyPair(to.certPEMBlock, to.keyPEMBlock)
		if err != nil {
			return nil, fmt.Errorf("unable to load cert from key pair: %w", err)
		}

		config.Certificates = []tls.Certificate{cert}
	}

	if len(to.caPEMBlock) > 0 {
		cp := x509.NewCertPool()
		if !cp.AppendCertsFromPEM(to.caPEMBlock) {
			return nil, fmt.Errorf("failed to append certificates from pem block")
		}

		config.RootCAs = cp
	}

	return &config, nil
}
