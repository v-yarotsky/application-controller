package k8s

import (
	"fmt"
	"os"
)

func CurrentNamespace() (string, error) {
	// Ref: https://stackoverflow.com/a/54374832
	namespaceBytes, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		return "", fmt.Errorf("failed to read current namespace: %w", err)
	}
	return string(namespaceBytes), nil
}
