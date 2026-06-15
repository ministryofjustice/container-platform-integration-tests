package integration_tests

import (
	"strings"

	"github.com/gruntwork-io/terratest/modules/k8s"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("GIVEN a gatekeeper installation", func() {
	options := k8s.NewKubectlOptions("", "", "gatekeeper-system")

	expectedConstraints := []string{
		"lockprivcapabilities",
		"k8srequiredlabels",
		"k8sservicetypeloadbalancer",
		"warnkubectlserviceaccount",
		"k8swarnserviceaccountsecretdelete",
	}

	It("THEN return all expected constraints", func() {
		actual, err := k8s.RunKubectlAndGetOutputE(
			GinkgoT(),
			options,
			"get",
			"constrainttemplates.templates.gatekeeper.sh",
			"-o",
			"jsonpath={range .items[*]}{.metadata.name}{'\\n'}{end}",
		)
		if err != nil {
			Fail(err.Error())
		}

		names := strings.Fields(actual)

		for _, expected := range expectedConstraints {
			Expect(names).To(ContainElement(expected))
		}
	})
})
