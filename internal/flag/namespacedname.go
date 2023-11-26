package flag

import (
	"flag"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/types"
)

type NamespacedName struct {
	Value types.NamespacedName
}

func (v *NamespacedName) String() string {
	return v.Value.String()
}

func (v *NamespacedName) Set(value string) error {
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
	*v = NamespacedName{types.NamespacedName{Namespace: namespace, Name: name}}
	return nil
}

var _ flag.Value = &NamespacedName{}
