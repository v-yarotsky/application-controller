package flag

import (
	"flag"
	"strings"
)

type StringSlice []string

func (v *StringSlice) String() string {
	return strings.Join(*v, ",")
}

func (v *StringSlice) Set(value string) error {
	*v = append(*v, value)
	return nil
}

var _ flag.Value = &StringSlice{}
