package integration_tests

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"time"
	"encoding/json"

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

            // Check Auth Mode is correct for Pod Identity
            out, err := eksClient.DescribeCluster(ctx, &eks.DescribeClusterInput{
                Name: aws.String(clusterName),
            })
            Expect(err).NotTo(HaveOccurred())  
            authMode := string(out.Cluster.AccessConfig.AuthenticationMode)
            fmt.Printf("Cluster authentication mode: %s\n", authMode)   
            Expect(authMode).To(
                Or(
                    Equal("API"),
                    Equal("API_AND_CONFIG_MAP"),
                ),
                "Cluster does not support Pod Identity (authentication mode not API-based)",
            )

            // Namespace
			namespace = "pod-identity-test"
			serviceAccountName = "pod-identity-test-sa"
			podName = "pod-identity-test-pod"
			options = k8s.NewKubectlOptions("", "", namespace)
			
			tpl, err := helpers.TemplateFile("./fixtures/namespace.yaml.tmpl", "namespace.yaml.tmpl", map[string]interface{}{
				"namespace": namespace,
				"psaMode":   "enforce",
			})
			Expect(err).NotTo(HaveOccurred())
			err = k8s.KubectlApplyFromStringE(GinkgoT(), options, tpl)
			Expect(err).NotTo(HaveOccurred())

            DeferCleanup(func() {
                fmt.Println("Cleaning up namespace...")
            
                err := k8s.DeleteNamespaceE(GinkgoT(), options, namespace)
                if err != nil {
                    fmt.Printf("Warning: failed to delete namespace: %v\n", err)
                }
            })

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

			DeferCleanup(func() {
                fmt.Println("Cleaning up Pod Identity association...")
            
                _, err := eksClient.DeletePodIdentityAssociation(ctx, &eks.DeletePodIdentityAssociationInput{
                    ClusterName:   aws.String(clusterName),
                    AssociationId: associationID,
                })
            
                if err != nil {
                    fmt.Printf("Warning: failed to delete association: %v\n", err)
                }
            })

            // Wait for the association to be created before creating the pod
            Eventually(func() error {
                out, err := eksClient.DescribePodIdentityAssociation(ctx, &eks.DescribePodIdentityAssociationInput{
                    ClusterName:   aws.String(clusterName),
                    AssociationId: associationID,
                })
                if err != nil {
                    return err
                }
                if out.Association == nil {
                    return fmt.Errorf("association not ready yet")
                }
                return nil
            }, 2*time.Minute, 5*time.Second).Should(Succeed())

            // Give control plane + node time to sync association
            time.Sleep(15 * time.Second)
		})

		AfterEach(func() {
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


        FIt("THEN the pod can assume the associated IAM role via the Pod Identity Agent", func() {
        
            roleName := (*podIdentityRoleArn)[strings.LastIndex(*podIdentityRoleArn, "/")+1:]
        
            // Step 1: create the pod immediately
            podTpl, err := helpers.TemplateFile("./fixtures/pod-identity-test-pod.yaml.tmpl", "pod-identity-test-pod.yaml.tmpl", map[string]interface{}{
                "podName":            podName,
                "namespace":          namespace,
                "serviceAccountName": serviceAccountName,   
            })
            Expect(err).NotTo(HaveOccurred())
        
            Expect(k8s.KubectlApplyFromStringE(GinkgoT(), options, podTpl)).To(Succeed())
        
            k8s.WaitUntilPodAvailable(GinkgoT(), options, podName, 5, 5*time.Second)
        
            var output string
        
            // Step 2: wait until identity becomes usable OR force restart
            Eventually(func() error {
        
                // Try calling STS
                out, err := k8s.RunKubectlAndGetOutputE(
                    GinkgoT(),
                    options,
                    "exec", podName, "--",
                    "aws", "sts", "get-caller-identity",
                )

                if err == nil {
                    type CallerIdentity struct {
                        Arn string `json:"Arn"`
                    }
                
                    var ci CallerIdentity
                    parseErr := json.Unmarshal([]byte(out), &ci)
                    if parseErr != nil {
                        return fmt.Errorf("failed to parse STS output: %v", parseErr)
                    }
                
                    // Strict check: split ARN resource part and compare exactly
                    parts := strings.Split(ci.Arn, ":")
                    if len(parts) < 6 {
                        return fmt.Errorf("invalid ARN format: %s", ci.Arn)
                    }
                
                    resource := parts[5] // e.g. "assumed-role/role/session"
                    segments := strings.Split(resource, "/")
                
                    if len(segments) < 3 {
                        return fmt.Errorf("invalid assumed-role ARN format: %s", ci.Arn)
                    }
                
                    actual := fmt.Sprintf("%s/%s/%s", segments[0], segments[1], "") // reconstruct prefix
                    expectedExact := fmt.Sprintf("assumed-role/%s/", roleName)

                    Expect(actual).To(Equal(expectedExact), fmt.Sprintf("role validation failed: expected '%s', got '%s' (full ARN: %s)", expectedExact, actual, ci.Arn,),)
                
                    output = out
                
                    fmt.Printf("Assumed role '%s' validated successfully.\nMatched exact segment: '%s'\nFull ARN: '%s'\n", roleName, expectedExact, ci.Arn,)
                
                    return nil
                }
        
                // Debug (optional but useful)
                fmt.Println("Pod identity not ready yet, restarting pod...")
        
                // Step 3: delete pod to force re-injection
                _, _ = k8s.RunKubectlAndGetOutputE(GinkgoT(), options, "delete", "pod", podName, "--ignore-not-found")
        
                // Step 4: recreate pod
                err = k8s.KubectlApplyFromStringE(GinkgoT(), options, podTpl)
                if err != nil {
                    return err
                }
        
                // Wait again
                k8s.WaitUntilPodAvailable(GinkgoT(), options, podName, 5, 5*time.Second)
        
                return fmt.Errorf("pod identity not ready yet")
        
            }, 2*time.Minute, 10*time.Second).Should(Succeed())
        
            fmt.Printf("get-caller-identity output: %s\n", output)
        })

	})
})
