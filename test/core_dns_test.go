package integration_tests

import (
	"fmt"
	"html/template"
	"strings"

	"github.com/gruntwork-io/terratest/modules/k8s"
	"github.com/gruntwork-io/terratest/modules/logger"
	"github.com/gruntwork-io/terratest/modules/random"
	"github.com/ministryofjustice/cloud-platform-integration-tests/test/helpers"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("core-dns", Serial, Ordered, func() {
	var (
		namespaceName string
		options       *k8s.KubectlOptions
		oldLogger     *logger.Logger
	)

	BeforeAll(func() {
		namespaceName = fmt.Sprintf(
			"%s-coredns-%s",
			c.Prefix,
			strings.ToLower(random.UniqueId()),
		)

		options = k8s.NewKubectlOptions("", "", namespaceName)

		oldLogger = options.Logger
		options.Logger = logger.Default

		GinkgoWriter.Println("STEP 1: rendering namespace template")

		nsTpl, err := helpers.TemplateFile(
			"./fixtures/namespace.yaml.tmpl",
			"namespace.yaml.tmpl",
			template.FuncMap{
				"namespace": namespaceName,
			},
		)
		Expect(err).NotTo(HaveOccurred())

		GinkgoWriter.Println("STEP 2: applying namespace")

		err = k8s.KubectlApplyFromStringE(
			GinkgoT(),
			options,
			nsTpl,
		)
		Expect(err).NotTo(HaveOccurred())

		GinkgoWriter.Println("STEP 3: rendering pod template")

		podTpl, err := helpers.TemplateFile(
			"./fixtures/unprivileged-pod.yaml.tmpl",
			"unprivileged-pod.yaml.tmpl",
			template.FuncMap{
				"namespace": namespaceName,
			},
		)
		Expect(err).NotTo(HaveOccurred())

		GinkgoWriter.Println("STEP 4: applying pod")

		err = k8s.KubectlApplyFromStringE(
			GinkgoT(),
			options,
			podTpl,
		)
		Expect(err).NotTo(HaveOccurred())

		GinkgoWriter.Println("STEP 5: waiting for pod readiness")

		Eventually(func() error {
			output, err := k8s.RunKubectlAndGetOutputE(
				GinkgoT(),
				options,
				"get",
				"pod",
				"unprivileged-integration-test",
				"-o",
				"jsonpath={.status.conditions[?(@.type=='Ready')].status}",
			)

			if err != nil {
				return err
			}

			if output != "True" {
				return fmt.Errorf("pod not ready yet: %s", output)
			}

			return nil
		}, "2m", "5s").Should(Succeed())
	})

	AfterAll(func() {
		err := k8s.DeleteNamespaceE(
			GinkgoT(),
			options,
			namespaceName,
		)
		Expect(err).NotTo(HaveOccurred())

		options.Logger = oldLogger
	})

	It("should resolve required hostnames", func() {

		tests := []struct {
			hostname   string
			recordType string
			expected   string
		}{
			{
				hostname:   "google.com",
				recordType: "",
				expected:   "google.com",
			},
			{
				hostname:   "user-guide.development.container-platform.service.justice.gov.uk",
				recordType: "",
				expected:   "ministryofjustice.github.io",
			},
			{
				hostname:   "octo-nonlive.container-platform.service.justice.gov.uk",
				recordType: "NS",
				expected:   "awsdns",
			},
		}

		for _, test := range tests {

			GinkgoWriter.Printf("Testing DNS resolution for %s\n", test.hostname)

			var output string

			Eventually(func() error {
				var err error

				args := []string{
					"exec",
					"pod/unprivileged-integration-test",
					"--",
					"nslookup",
				}

				if test.recordType != "" {
					args = append(args, "-type="+test.recordType)
				}

				args = append(args, test.hostname)

				output, err = k8s.RunKubectlAndGetOutputE(
					GinkgoT(),
					options,
					args...,
				)

				return err
			}, "2m", "5s").Should(Succeed())

			Expect(output).To(
				ContainSubstring(test.expected),
				"hostname: %s\noutput:\n%s",
				test.hostname,
				output,
			)
		}
	})
})
