package testutil

import (
	gtypes "github.com/onsi/gomega/types"
)

type TestingT interface {
	gtypes.GomegaTestingT
	Cleanup(func())
}
