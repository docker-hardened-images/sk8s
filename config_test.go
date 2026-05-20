package sk8s

import (
	"strings"
	"testing"
)

func TestClusterConfig_Marshall_includesDisable(t *testing.T) {
	t.Parallel()
	b, err := (&ClusterConfig{
		Disable: []string{"metrics-server"},
	}).Marshall()
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "metrics-server") {
		t.Fatalf("expected disable list in YAML, got:\n%s", s)
	}
}

func TestDefaultConfig_Marshall(t *testing.T) {
	t.Parallel()
	b, err := DefaultConfig.Marshall()
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 {
		t.Fatal("expected non-empty YAML")
	}
}
