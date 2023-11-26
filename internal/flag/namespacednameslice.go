package flag

import (
	"flag"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/types"
)

type NamespacedNameSlice []types.NamespacedName

func (v *NamespacedNameSlice) String() string {
	parts := make([]string, 0, len(*v))
	for _, n := range *v {
		parts = append(parts, n.String())
	}
	return strings.Join(parts, ",")
}

func (v *NamespacedNameSlice) Set(value string) error {
	if value == "" {
		return fmt.Errorf("invalid namespaced name: %q", value)
	}

	parts := strings.SplitN(value, "/", 2)
	var namespace, name string
	switch len(parts) {
	case 1:
		namespace = ""
		name = parts[0]
	case 2:
		namespace = parts[0]
		name = parts[1]
	}
	*v = append(*v, types.NamespacedName{Namespace: namespace, Name: name})
	return nil
}

var _ flag.Value = &NamespacedNameSlice{}
