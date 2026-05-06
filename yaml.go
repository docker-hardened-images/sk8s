package sk8s

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/restmapper"
)

// ApplyYAMLFile should act like `kubectl apply -f <file>`.
// fieldManager should be a string that identifies the caller, usually "test".
func (c *TestCluster) ApplyYAMLFile(ctx context.Context, yamlFile string, fieldManager string, extraSchema ...runtime.SchemeBuilder) error {
	yamlData, err := os.ReadFile(yamlFile)
	if err != nil {
		return fmt.Errorf("failed to read YAML file: %v", err)
	}

	return c.ApplyYAMLData(ctx, yamlData, fieldManager, extraSchema...)
}

// ApplyYAMLData should act like `kubectl apply -f <file>`, but for a yaml doc already in memory.
// fieldManager should be a string that identifies the caller, usually "test".
func (c *TestCluster) ApplyYAMLData(ctx context.Context, yamlData []byte, fieldManager string, extraSchema ...runtime.SchemeBuilder) error {
	yamlDocs := splitYAMLDocuments(yamlData)

	discoveryClient := c.Client().Discovery()
	groupResources, err := restmapper.GetAPIGroupResources(discoveryClient)
	if err != nil {
		return fmt.Errorf("failed to get API group resources: %v", err)
	}

	restMapper := restmapper.NewDiscoveryRESTMapper(groupResources)

	if err := apiextensionsv1.AddToScheme(scheme.Scheme); err != nil {
		return fmt.Errorf("failed to add apiextensions to scheme: %v", err)
	}
	for _, s := range extraSchema {
		if err := s.AddToScheme(scheme.Scheme); err != nil {
			return fmt.Errorf("failed to add to scheme: %v", err)
		}
	}
	decode := serializer.NewCodecFactory(scheme.Scheme).UniversalDeserializer().Decode

	for _, yamlDoc := range yamlDocs {
		if len(strings.TrimSpace(yamlDoc)) == 0 {
			continue // Skip empty documents
		}

		obj, gvk, err := decode([]byte(yamlDoc), nil, nil)
		if err != nil {
			return fmt.Errorf("failed to decode YAML: %v", err)
		}

		unstructuredMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
		if err != nil {
			return fmt.Errorf("failed to convert to unstructured: %v", err)
		}
		unstructuredObj := &unstructured.Unstructured{Object: unstructuredMap}

		mapping, err := restMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			return fmt.Errorf("failed to get REST mapping: %v", err)
		}

		dynamicClient, err := c.DynamicClient(ctx)
		if err != nil {
			return nil
		}

		var resourceInterface dynamic.ResourceInterface
		if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
			// Namespaced resource
			namespace := unstructuredObj.GetNamespace()
			if namespace == "" {
				namespace = "default"
			}
			resourceInterface = dynamicClient.Resource(mapping.Resource).Namespace(namespace)
		} else {
			// Cluster-scoped resource
			resourceInterface = dynamicClient.Resource(mapping.Resource)
		}

		err = applyResource(ctx, resourceInterface, unstructuredObj, fieldManager)
		if err != nil {
			return fmt.Errorf("failed to apply resource %s/%s: %v",
				unstructuredObj.GetKind(), unstructuredObj.GetName(), err)
		}

		logger := log.Default()
		logger.Printf("Applied %s/%s", unstructuredObj.GetKind(), unstructuredObj.GetName())
	}

	return nil
}

// ApplyRemoteYAMLs downloads and applies yamls from remote URLs using kubectl
// no need to parse multiple "---" documents as kubectl apply handles them natively
func (c *TestCluster) ApplyRemoteYAMLs(ctx context.Context, urls []string) error {
	for _, url := range urls {
		fmt.Printf("Applying YAML from %s\n", url)
		_, stderr, err := c.cluster.Exec(ctx, []string{
			"kubectl", "apply", "--server-side", "-f", url,
		})

		if err != nil {
			return fmt.Errorf("failed to apply YAML from %s: %w (stderr: %s)", url, err, stderr)
		}
	}

	return nil
}

// ApplyLocalYAMLs copies local YAML files into the cluster and applies them using kubectl.
// This is useful for CRDs and custom resources that may not be registered in the Go scheme.
// Unlike ApplyYAMLFile, this method uses kubectl directly, which handles any resource type.
func (c *TestCluster) ApplyLocalYAMLs(ctx context.Context, yamlFiles []string) error {
	for _, yamlFile := range yamlFiles {
		// Copy file to container
		containerPath := "/tmp/" + filepath.Base(yamlFile)
		err := c.cluster.CopyFileToContainer(ctx, yamlFile, containerPath, 0o644)
		if err != nil {
			return fmt.Errorf("failed to copy YAML file %s to container: %w", yamlFile, err)
		}

		// Apply using kubectl inside the container
		fmt.Printf("Applying YAML from %s\n", yamlFile)
		_, stderr, err := c.cluster.Exec(ctx, []string{
			"kubectl", "apply", "--server-side", "-f", containerPath,
		})
		if err != nil {
			return fmt.Errorf("failed to apply YAML from %s: %w (stderr: %s)", yamlFile, err, stderr)
		}
		b, err := io.ReadAll(stderr)
		if err != nil {
			return fmt.Errorf("apply failed for file %s to container: %w", yamlFile, err)
		}
		o := string(b)
		fmt.Print(o)
	}

	return nil
}

// splitYAMLDocuments splits a multi-document YAML file into separate strings
func splitYAMLDocuments(yamlData []byte) []string {
	var documents []string
	scanner := bufio.NewScanner(bytes.NewReader(yamlData))
	var currentDoc strings.Builder

	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			if currentDoc.Len() > 0 {
				documents = append(documents, currentDoc.String())
				currentDoc.Reset()
			}
		} else {
			currentDoc.WriteString(line + "\n")
		}
	}

	// Add the last document
	if currentDoc.Len() > 0 {
		documents = append(documents, currentDoc.String())
	}

	return documents
}

// applyResource applies a single resource using server-side apply
func applyResource(ctx context.Context, resourceInterface dynamic.ResourceInterface, obj *unstructured.Unstructured, fieldManager string) error {
	applyOptions := metav1.ApplyOptions{
		FieldManager: fieldManager,
		Force:        true,
	}

	_, err := resourceInterface.Apply(ctx, obj.GetName(), obj, applyOptions)
	if err != nil {
		return fmt.Errorf("server-side apply failed: %v", err)
	}

	return nil
}
