package integration_tests

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/eks"

	"github.com/gruntwork-io/terratest/modules/k8s"
	"github.com/ministryofjustice/cloud-platform-integration-tests/test/helpers"

	"k8s.io/client-go/tools/clientcmd"
)

const podIdentityLabel = "pod-identity"

var podIdentityRoleArn = flag.String("podIdentityRoleArn", "", "(pod identity tests) ARN of an IAM role trusting pods.eks.amazonaws.com, used to test EKS Pod Identity")

var _ = Describe("EKS Pod Identity Agent", Label(podIdentityLabel), func() {

	Context("Pod Identity association", func() {

		var (
			ctx                context.Context
			clusterName        string
			eksClient          *eks.Client
			namespace          string
			serviceAccountName string
			podName            string
			options            *k8s.KubectlOptions
			associationID      *string
		)

		BeforeEach(func() {
			if *podIdentityRoleArn == "" {
				Skip("-podIdentityRoleArn was not set - skipping functional Pod Identity test")
			}

			ctx = context.Background()

			kubeconfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
				clientcmd.NewDefaultClientConfigLoadingRules(),
				&clientcmd.ConfigOverrides{},
			)
			rawConfig, err := kubeconfig.RawConfig()
			Expect(err).ToNot(HaveOccurred())
			currentContext := rawConfig.CurrentContext
			Expect(currentContext).ToNot(BeEmpty())
			clusterRef := rawConfig.Contexts[currentContext].Cluster
			parts := strings.Split(clusterRef, "/")
			clusterName = parts[len(parts)-1]

			awsCfg, err := config.LoadDefaultConfig(ctx,
				config.WithRegion("eu-west-2"),
			)
			Expect(err).ToNot(HaveOccurred())
			eksClient = eks.NewFromConfig(awsCfg)

			namespace = "pod-identity-test"
			serviceAccountName = "pod-identity-test-sa"
			podName = "pod-identity-test-pod"
			options = k8s.NewKubectlOptions("", "", namespace)

			// Namespace
			tpl, err := helpers.TemplateFile("./fixtures/namespace.yaml.tmpl", "namespace.yaml.tmpl", map[string]interface{}{
				"namespace": namespace,
				"psaMode":   "enforce",
			})
			Expect(err).NotTo(HaveOccurred())
			err = k8s.KubectlApplyFromStringE(GinkgoT(), options, tpl)
			Expect(err).NotTo(HaveOccurred())

			// ServiceAccount
			_, err = k8s.RunKubectlAndGetOutputE(GinkgoT(), options, "create", "serviceaccount", serviceAccountName)
			Expect(err).NotTo(HaveOccurred())

			// Pod Identity Association
			assocOut, err := eksClient.CreatePodIdentityAssociation(ctx, &eks.CreatePodIdentityAssociationInput{
				ClusterName:    aws.String(clusterName),
				Namespace:      aws.String(namespace),
				ServiceAccount: aws.String(serviceAccountName),
				RoleArn:        aws.String(*podIdentityRoleArn),
			})
			Expect(err).NotTo(HaveOccurred())
			associationID = assocOut.Association.AssociationId

			// Pod that uses the ServiceAccount
			podTpl, err := helpers.TemplateFile("./fixtures/pod-identity-test-pod.yaml.tmpl", "pod-identity-test-pod.yaml.tmpl", map[string]interface{}{
				"podName":            podName,
				"namespace":          namespace,
				"serviceAccountName": serviceAccountName,
			})
			Expect(err).NotTo(HaveOccurred())
			err = k8s.KubectlApplyFromStringE(GinkgoT(), options, podTpl)
			Expect(err).NotTo(HaveOccurred())

			k8s.WaitUntilPodAvailable(GinkgoT(), options, podName, 12, 10*time.Second)
		})

		AfterEach(func() {
			if CurrentSpecReport().Failed() {
				fmt.Println("Test failed — sleeping 120s for debugging...")
				time.Sleep(120 * time.Second)
			}

			if eksClient != nil && associationID != nil {
				_, _ = eksClient.DeletePodIdentityAssociation(ctx, &eks.DeletePodIdentityAssociationInput{
					ClusterName:   aws.String(clusterName),
					AssociationId: associationID,
				})
			}
			if options != nil {
				_ = k8s.DeleteNamespaceE(GinkgoT(), options, namespace)
			}
		})

		It("THEN the pod can assume the associated IAM role via the Pod Identity Agent", func() {
			output, err := k8s.RunKubectlAndGetOutputE(GinkgoT(), options, "exec", podName, "--", "aws", "sts", "get-caller-identity")
			Expect(err).NotTo(HaveOccurred(), "pod failed to retrieve AWS credentials from the Pod Identity Agent")

			fmt.Printf("get-caller-identity output: %s\n", output)

			roleName := (*podIdentityRoleArn)[strings.LastIndex(*podIdentityRoleArn, "/")+1:]
			Expect(output).To(ContainSubstring(fmt.Sprintf("assumed-role/%s/", roleName)))
		})
	})
})
