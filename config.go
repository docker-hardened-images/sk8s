package sk8s

import (
	"gopkg.in/yaml.v3"
)

type ClusterConfig struct {
	APIServerArg         []string `yaml:"kube-apiserver-arg,omitempty"`
	KubeletArg           []string `yaml:"kubelet-arg,omitempty"`
	Disable              []string `yaml:"disable"`
	FlannelBackEnd       string   `yaml:"flannel-backend,omitempty"`
	ClusterCIRD          string   `yaml:"cluster-cidr,omitempty"`
	DisableNetworkPolicy bool     `yaml:"disable-network-policy,omitempty"`
	PauseImage           string   `yaml:"pause-image,omitempty"`
}

func (c *ClusterConfig) Marshall() ([]byte, error) {
	return yaml.Marshal(c)
}
