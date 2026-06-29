package integration_tests

//  Setup code with common BeforeSuite for all tests
//  Initialises Kubernetes clients from the local kubeconfig and prepares the test environment by annotating the default namespace to permit LoadBalancer services

import (
    "context"

    . "github.com/onsi/ginkgo/v2"
    . "github.com/onsi/gomega"

    "k8s.io/client-go/kubernetes"
    "k8s.io/client-go/rest"
    "k8s.io/client-go/tools/clientcmd"

    corev1 "k8s.io/api/core/v1"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

    storageclient "k8s.io/client-go/kubernetes/typed/storage/v1"
)

var (
    clientset *kubernetes.Clientset
    storageClient *storageclient.StorageV1Client
    kubeConfig    *rest.Config
    ctx       context.Context
)

var _ = BeforeSuite(func() {
	// Load local Kubernetes configuration and create client
    ctx = context.Background()

    kubeConfigLoader := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
        clientcmd.NewDefaultClientConfigLoadingRules(),
        &clientcmd.ConfigOverrides{},
    )

    var err error
    kubeConfig, err = kubeConfigLoader.ClientConfig()
    Expect(err).ToNot(HaveOccurred())

    clientset, err = kubernetes.NewForConfig(kubeConfig)
    Expect(err).ToNot(HaveOccurred())

    // Create the Storage Client
    storageClient, err = storageclient.NewForConfig(kubeConfig)
    Expect(err).ToNot(HaveOccurred())

    // Annotate the default namespace to allow Load Balancer Services - REQUIRED by Gatekeeper
    namespace := &corev1.Namespace{
        ObjectMeta: metav1.ObjectMeta{
            Name: "default",
            Annotations: map[string]string{
                "container-platform.justice.gov.uk/can-use-loadbalancer-services": "true",
            },
        },
    }

    _, err = clientset.CoreV1().Namespaces().Update(ctx, namespace, metav1.UpdateOptions{})
    Expect(err).ToNot(HaveOccurred())
})