package integration_tests

import (
    "context"

    . "github.com/onsi/ginkgo/v2"
    . "github.com/onsi/gomega"

    "k8s.io/client-go/kubernetes"
    "k8s.io/client-go/tools/clientcmd"
)

var (
    clientset *kubernetes.Clientset
    ctx       context.Context
)

var _ = BeforeSuite(func() {
	// Load local Kubernetes configuration and create client
    ctx = context.Background()

    kubeconfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
        clientcmd.NewDefaultClientConfigLoadingRules(),
        &clientcmd.ConfigOverrides{},
    )

    config, err := kubeconfig.ClientConfig()
    Expect(err).ToNot(HaveOccurred())

    clientset, err = kubernetes.NewForConfig(config)
    Expect(err).ToNot(HaveOccurred())
})