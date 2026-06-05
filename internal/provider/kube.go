package provider

import (
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// buildKubeClient builds a dynamic Kubernetes client from the given kubeconfig path.
// If kubeconfigPath is empty, it tries KUBECONFIG env var, then ~/.kube/config, then in-cluster config.
func buildKubeClient(kubeconfigPath string) (dynamic.Interface, error) {
	config, err := buildRestConfig(kubeconfigPath)
	if err != nil {
		return nil, err
	}

	client, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("building dynamic client: %w", err)
	}
	return client, nil
}

// buildRestConfig resolves a *rest.Config from the kubeconfig path.
func buildRestConfig(kubeconfigPath string) (*rest.Config, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfigPath != "" {
		loadingRules.ExplicitPath = kubeconfigPath
	} else if kc := os.Getenv("KUBECONFIG"); kc != "" {
		loadingRules.ExplicitPath = kc
	} else {
		home, err := os.UserHomeDir()
		if err == nil {
			defaultKubeconfig := filepath.Join(home, ".kube", "config")
			if _, statErr := os.Stat(defaultKubeconfig); statErr == nil {
				loadingRules.ExplicitPath = defaultKubeconfig
			}
		}
	}

	configOverrides := &clientcmd.ConfigOverrides{}
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

	config, err := kubeConfig.ClientConfig()
	if err != nil {
		// Fall back to in-cluster config
		inCluster, inClusterErr := rest.InClusterConfig()
		if inClusterErr != nil {
			return nil, fmt.Errorf("building REST config from kubeconfig (%v) and in-cluster (%v): %w", err, inClusterErr, err)
		}
		return inCluster, nil
	}
	return config, nil
}
