package flag

import (
	"encoding/json"
	"flag"
	"fmt"
)

type JSONStringMap map[string]string

func (v *JSONStringMap) String() string {
	serialized, err := json.MarshalIndent(*v, "", "  ")
	if err != nil {
		return fmt.Sprintf("Invalid JSON: %v", *v)
	}
	return string(serialized)
}

func (v *JSONStringMap) Set(value string) error {
	err := json.Unmarshal([]byte(value), v)
	if err != nil {
		return fmt.Errorf("failed to set JSON flag: %w", err)
	}
	return nil
}

var _ flag.Value = &StringSlice{}
