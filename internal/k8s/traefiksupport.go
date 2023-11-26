package k8s

import (
	"fmt"

	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
)

func IsTraefikInstalled(cfg *rest.Config) (bool, error) {
	// Ref: https://github.com/kubernetes-sigs/kubebuilder/pull/3055#discussion_r1032753706

	var err error

	// Get a config to talk to the apiserver
	if cfg == nil {
		cfg, err = rest.InClusterConfig()
		if err != nil {
			return false, fmt.Errorf("unable to get kubernetes client config: %w", err)
		}
	}

	// Create the discoveryClient
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return false, fmt.Errorf("unable to create discovery client: %w", err)
	}

	// Get a list of all API's on the cluster
	_, apiResourceList, err := discoveryClient.ServerGroupsAndResources()
	if err != nil {
		return false, fmt.Errorf("unable to get Group and Resources: %w", err)
	}

	for _, g := range apiResourceList {
		if g.GroupVersion != "traefik.io/v1alpha1" {
			continue
		}

		for _, r := range g.APIResources {
			if r.Kind == "IngressRoute" {
				return true, nil
			}
		}
	}

	return false, nil
}
