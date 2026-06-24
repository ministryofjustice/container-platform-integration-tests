package integration_tests

import (
    "context"
    "fmt"
    "strings"

    . "github.com/onsi/ginkgo/v2"
    . "github.com/onsi/gomega"

    "github.com/aws/aws-sdk-go-v2/config"
    "github.com/aws/aws-sdk-go-v2/service/eks"

    "k8s.io/client-go/tools/clientcmd"
)

var _ = Describe("EKS Auto Mode", func() {
    It("should have computeConfig.enabled = true", func() {
        ctx := context.Background()

        // --- Get cluster name from kubeconfig ---
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
        clusterName := parts[len(parts)-1]
        fmt.Printf("Config View: %s\n", clusterName)

        //--- AWS SDK setup ---
        awsCfg, err := config.LoadDefaultConfig(ctx)
        Expect(err).ToNot(HaveOccurred())
        eksClient := eks.NewFromConfig(awsCfg)

        //--- Describe cluster ---
        out, err := eksClient.DescribeCluster(ctx, &eks.DescribeClusterInput{
            Name: &clusterName,
        })


        //--- Get  Compute Config and Test If It Is Enabled---
        computeEnabled := "UNKNOWN"
        if err == nil && out.Cluster != nil && out.Cluster.ComputeConfig != nil && out.Cluster.ComputeConfig.Enabled != nil {
            if *out.Cluster.ComputeConfig.Enabled {
                computeEnabled = "True"
            } else {
                computeEnabled = "False"
            }
        }
        Expect(computeEnabled).To(Equal("True"))
    })
})