package gh

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestGH(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "GH Suite")
}
