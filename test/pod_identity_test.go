package integration_tests

// Test for Pod Identity, includes tests that:
//   Authorisation mode is one of API_AND_CONFIG_MAP or API_AND_CONFIG_MAP
//   A pod can be created with the right inserted credentails for Pod Identity
//   A role can be assumed with Pod Identity
// The test creates and then deletes a temporary role called container-platform-temp-integration-test-role though this can be overridden with an input flag

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
    "github.com/aws/aws-sdk-go-v2/service/iam"

    "github.com/gruntwork-io/terratest/modules/k8s"
    "github.com/ministryofjustice/cloud-platform-integration-tests/test/helpers"

    "k8s.io/client-go/tools/clientcmd"
)

const podIdentityLabel = "pod-identity"


// Collect the Role ARN from input flag - this can be used to override creation of a temporary role
// IAM role must trust pods.eks.amazonaws.com and not be assumable by node roles
// Pass in the format -podIdentityRoleArn=arn:aws:iam::<account_number>:role/<role-name>
var podIdentityRoleArn = flag.String("podIdentityRoleArn", "", "(pod identity tests) ARN of an IAM role trusting pods.eks.amazonaws.com, used to test EKS Pod Identity")

func awsString(s string) *string {
    return &s
}

func awsInt32(i int32) *int32 {
    return &i
}

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
            // Vars moved from wider context
            iamClient *iam.Client
            temporaryRoleName  = "container-platform-temp-integration-test-role"        
            createTemporaryIAMRole    bool
            resolvedPodIdentityArn    string
            roleName                  string
        )



        BeforeEach(func() {

            if *podIdentityRoleArn == "" {
                createTemporaryIAMRole = true
            } else {
                createTemporaryIAMRole = false
                resolvedPodIdentityArn = *podIdentityRoleArn
            }

            ctx = context.Background()

            // Get Kube and AWS config
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

            // Create temporary IAM test role
            if createTemporaryIAMRole {
                iamClient = iam.NewFromConfig(awsCfg)
                assumeRolePolicy := `{
                    "Version": "2012-10-17",
                    "Statement": [
                        {
                            "Effect": "Allow",
                            "Principal": {
                                "Service": "pods.eks.amazonaws.com"
                            },
                            "Action": [
                                "sts:AssumeRole",
                                "sts:TagSession"
                            ]
                        }
                    ]
                }`
                output, err := iamClient.CreateRole(ctx, &iam.CreateRoleInput{
                    RoleName:                 &temporaryRoleName,
                    AssumeRolePolicyDocument: &assumeRolePolicy,
                    Description:              awsString("Test role for EKS Pod Identity Agent validation"),
                    MaxSessionDuration:       awsInt32(3600),
                })
                Expect(err).ToNot(HaveOccurred())
                resolvedPodIdentityArn = *output.Role.Arn
            }

            // Initialise EKS client for Pod Identity API operations
            eksClient = eks.NewFromConfig(awsCfg)

            // Check Auth Mode is correct for Pod Identity
            out, err := eksClient.DescribeCluster(ctx, &eks.DescribeClusterInput{
                Name: aws.String(clusterName),
            })
            Expect(err).NotTo(HaveOccurred())  
            authMode := string(out.Cluster.AccessConfig.AuthenticationMode)

            // Validate authentication mode
            fmt.Printf("Cluster authentication mode: %s\n", authMode)   
            Expect(authMode).To(
                Or(
                    Equal("API"),
                    Equal("API_AND_CONFIG_MAP"),
                ),
                "Cluster does not support Pod Identity (authentication mode not API-based)",
            )

            // Namespace variables
            namespace = "pod-identity-test"
            serviceAccountName = "pod-identity-test-sa"
            podName = "pod-identity-test-pod"
            options = k8s.NewKubectlOptions("", "", namespace)
            
            // Apply namespace with Pod Security Admission enabled t
            tpl, err := helpers.TemplateFile("./fixtures/namespace.yaml.tmpl", "namespace.yaml.tmpl", map[string]interface{}{
                "namespace": namespace,
                "psaMode":   "enforce",
            })
            Expect(err).NotTo(HaveOccurred())
            err = k8s.KubectlApplyFromStringE(GinkgoT(), options, tpl)
            Expect(err).NotTo(HaveOccurred())

            // Fallback cleanup; may already be deleted by previous runs
            DeferCleanup(func() {
                fmt.Println("Fallback cleaning up namespace...")
            
                err := k8s.DeleteNamespaceE(GinkgoT(), options, namespace)
                if err != nil {
                    fmt.Printf("Warning: failed to delete namespace: %v\n", err)
                }
            })

            // Create ServiceAccount
            _, err = k8s.RunKubectlAndGetOutputE(GinkgoT(), options, "create", "serviceaccount", serviceAccountName)
            Expect(err).NotTo(HaveOccurred())

            // Pod Identity Association. Links Kubernetes ServiceAccount to IAM role via EKS control plane
            assocOut, err := eksClient.CreatePodIdentityAssociation(ctx, &eks.CreatePodIdentityAssociationInput{
                ClusterName:    aws.String(clusterName),
                Namespace:      aws.String(namespace),
                ServiceAccount: aws.String(serviceAccountName),
                RoleArn:        aws.String(resolvedPodIdentityArn),
            })
            Expect(err).NotTo(HaveOccurred())
            associationID = assocOut.Association.AssociationId

            // Fallback cleanup; may already be deleted by previous runs
            DeferCleanup(func() {
                fmt.Println("Fallback cleaning up Pod Identity association...")
            
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

            // Give control plane to propagation to node before pod creation to avoid race condition
            time.Sleep(15 * time.Second)
        })



        AfterEach(func() {
            fmt.Printf("Cleaning up association and namespace")
            if eksClient != nil && associationID != nil {
                _, _ = eksClient.DeletePodIdentityAssociation(ctx, &eks.DeletePodIdentityAssociationInput{
                    ClusterName:   aws.String(clusterName),
                    AssociationId: associationID,
                })
            }
            if options != nil {
                _ = k8s.DeleteNamespaceE(GinkgoT(), options, namespace)
            }

            // Delete temporary IAM test role if it was created
            if createTemporaryIAMRole {
                _, err := iamClient.DeleteRole(ctx, &iam.DeleteRoleInput{
                    RoleName: &temporaryRoleName,
                })
                Expect(err).ToNot(HaveOccurred())
                // Confirm deletion (IAM is eventually consistent)    
                Eventually(func() error {
                    _, err := iamClient.GetRole(ctx, &iam.GetRoleInput{
                        RoleName: &temporaryRoleName,
                    })
                    return err
                }).Should(MatchError(ContainSubstring("NoSuchEntity")))
            }
        })



        It("THEN the pod can assume the associated IAM role via the Pod Identity Agent", func() {
        
            // Calculate the temporary role name
            if createTemporaryIAMRole {
                roleName = temporaryRoleName
            } else {
                roleName = (resolvedPodIdentityArn)[strings.LastIndex(resolvedPodIdentityArn, "/")+1:]
            }

            // Check and print the ARN being used
            if createTemporaryIAMRole {
                fmt.Println("INFO: podIdentityRoleArn flag not set. Test will create and delete a temporary IAM role")
            } else {
                fmt.Println("INFO: podIdentityRoleArn flag set, using this IAM role for test")
            }
            fmt.Printf("INFO: Using Pod Identity Role ARN: %s\n", resolvedPodIdentityArn)
            fmt.Printf("INFO: Using Role Name: %s\n", roleName)

            // Step 1: Create the pod immediately, loop through pod creation/deletion to allow for timing failures (e.g. variable injection)
            //         Pod creation triggers credential injection attempt by Pod Identity agent
            //         Pod spec must explicitly bind to test ServiceAccount
            podTpl, err := helpers.TemplateFile("./fixtures/pod-identity-test-pod.yaml.tmpl", "pod-identity-test-pod.yaml.tmpl", map[string]interface{}{
                "podName":            podName,
                "namespace":          namespace,
                "serviceAccountName": serviceAccountName,   
            })
            Expect(err).NotTo(HaveOccurred())
        
            Expect(k8s.KubectlApplyFromStringE(GinkgoT(), options, podTpl)).To(Succeed())
        
            k8s.WaitUntilPodAvailable(GinkgoT(), options, podName, 5, 5*time.Second)
        
            // Step 2: Loop: wait until identity becomes usable OR force restart
            Eventually(func() error {
                // First Check Pod Identity environment variable injection
                env, err := k8s.RunKubectlAndGetOutputE(
                    GinkgoT(),
                    options,
                    "exec", podName, "--", "env",
                )
                if err != nil {
                    return err
                }
                // Test expected env variables to ensure Pod identity is being used
                // Pod Identity variables expected to be present
                Expect(strings.Contains(env, "AWS_CONTAINER_CREDENTIALS_FULL_URI")).To(BeTrue())
                Expect(strings.Contains(env, "AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE")).To(BeTrue())
                // IRSA variables expected to not be present
                Expect(strings.Contains(env, "AWS_WEB_IDENTITY_TOKEN_FILE")).To(BeFalse())
                Expect(strings.Contains(env, "AWS_ROLE_ARN")).To(BeFalse())
        
                // Step 2a: Try calling STS
                out, err := k8s.RunKubectlAndGetOutputE(
                    GinkgoT(),
                    options,
                    "exec", podName, "--",
                    "aws", "sts", "get-caller-identity",
                )

                //Step 2b: Validate Role
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
                
                    // Reconstruct prefix for strict comparison
                    actual := fmt.Sprintf("%s/%s/%s", segments[0], segments[1], "")
                    expectedExact := fmt.Sprintf("assumed-role/%s/", roleName)

                    // Strict role match, prevents false positives from similarly named roles
                    Expect(actual).To(Equal(expectedExact), fmt.Sprintf("role validation failed: expected '%s', got '%s' (full ARN: %s)", expectedExact, actual, ci.Arn,),)
                
                    // Useful DEBUG for successful test 
                    fmt.Printf("DEBUG Assumed role '%s' validated successfully.\nMatched exact segment: '%s'\nExpected exact segment: '%s'\nFull ARN: '%s'\n", roleName, actual, expectedExact, ci.Arn,)
                
                    return nil
                }

                // Step 2c: If Pod hasn't got the right role yet, delete pod to force re-injection
                fmt.Println("Pod identity not ready yet, restarting pod...")
                _, _ = k8s.RunKubectlAndGetOutputE(GinkgoT(), options, "delete", "pod", podName, "--ignore-not-found")
        
                // Step 2d: recreate pod
                err = k8s.KubectlApplyFromStringE(GinkgoT(), options, podTpl)
                if err != nil {
                    return err
                }
        
                // Wait again
                k8s.WaitUntilPodAvailable(GinkgoT(), options, podName, 5, 5*time.Second)
        
                return fmt.Errorf("pod identity not ready yet")
        
            }, 2*time.Minute, 10*time.Second).Should(Succeed())
        })
    })
})
