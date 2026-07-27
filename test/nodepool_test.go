package integration_tests

// Test for Node Pool tolerations and node selection, includes tests that:
//   1. An application with system-node tolerations runs on nodes with label "container-platform.justice.gov.uk/system-ng": "true"
//   2. An application with no tolerations runs on nodes with label "container-platform.justice.gov.uk/default-ng": "true"

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const nodePoolLabel = "nodepool"

var _ = Describe("Node Pool Tolerations and Node Selection", Label(nodePoolLabel), func() {

	Context("System node pool with tolerations", func() {

		var (
			testContext   context.Context
			namespace     string
			podName       string
			deploymentObj *corev1.Pod
		)

		BeforeEach(func() {
			testContext = context.Background()
			namespace = "nodepool-system-test"

			// Create test namespace
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: namespace,
				},
			}

			_, err := clientset.CoreV1().Namespaces().Create(testContext, ns, metav1.CreateOptions{})
			if err != nil {
				// Namespace may already exist, that's okay
				fmt.Printf("Note: namespace creation returned: %v\n", err)
			}

			// Cleanup on completion
			DeferCleanup(func() {
				fmt.Println("Cleaning up nodepool-system-test namespace...")
				_ = clientset.CoreV1().Namespaces().Delete(testContext, namespace, metav1.DeleteOptions{})
			})
		})

		It("THEN an application with system-node tolerations runs on a node with label container-platform.justice.gov.uk/system-ng=true", func() {
			podName = "nodepool-system-test-pod"

			// Create pod with system-node tolerations
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: namespace,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "test-container",
							Image: "busybox:latest",
							Command: []string{
								"sh", "-c",
								"sleep 3600",
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    *getResourceQuantity("100m"),
									corev1.ResourceMemory: *getResourceQuantity("64Mi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    *getResourceQuantity("200m"),
									corev1.ResourceMemory: *getResourceQuantity("128Mi"),
								},
							},
						},
					},
					// Select Sysgtem Node
					NodeSelector: map[string]string{
						"container-platform.justice.gov.uk/system-ng": "true",
					},
					// Add system-node tolerations
					Tolerations: []corev1.Toleration{
						{
							Key:      "system-node",
							Operator: corev1.TolerationOpEqual,
							Value:    "true",
							Effect:   corev1.TaintEffectNoSchedule,
						},
					},
					RestartPolicy: corev1.RestartPolicyNever,
				},
			}

			// Create the pod
			deploymentObj, err := clientset.CoreV1().Pods(namespace).Create(testContext, pod, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			// Wait for pod to be running
			Eventually(func() error {
				pod, err := clientset.CoreV1().Pods(namespace).Get(testContext, podName, metav1.GetOptions{})
				if err != nil {
					return err
				}
				if pod.Status.Phase != corev1.PodRunning {
					return fmt.Errorf("pod phase is %s, waiting for Running", pod.Status.Phase)
				}
				return nil
			}, 60*time.Second, 5*time.Second).Should(Succeed())

			// Get the pod's node
			pod, err := clientset.CoreV1().Pods(namespace).Get(testContext, podName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())

			nodeName := pod.Spec.NodeName
			Expect(nodeName).NotTo(BeEmpty())
			fmt.Printf("Pod scheduled on node: %s\n", nodeName)

			// Get the node and verify it has the correct label
			node, err := clientset.CoreV1().Nodes().Get(testContext, nodeName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())

			// Check for the system-ng label
			labels := node.GetLabels()
			systemNgLabel := labels["container-platform.justice.gov.uk/system-ng"]
			Expect(systemNgLabel).To(Equal("true"), "Node should have label container-platform.justice.gov.uk/system-ng=true")

			fmt.Printf("SUCCESS: Pod with system-node tolerations is running on node with correct label\n")
			fmt.Printf("Node labels: %v\n", labels)

			// Cleanup the pod
			_ = clientset.CoreV1().Pods(namespace).Delete(testContext, podName, metav1.DeleteOptions{})
		})
	})

	Context("Default node pool without tolerations", func() {

		var (
			testContext   context.Context
			namespace     string
			podName       string
			deploymentObj *corev1.Pod
		)

		BeforeEach(func() {
			testContext = context.Background()
			namespace = "nodepool-default-test"

			// Create test namespace
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: namespace,
				},
			}

			_, err := clientset.CoreV1().Namespaces().Create(testContext, ns, metav1.CreateOptions{})
			if err != nil {
				// Namespace may already exist, that's okay
				fmt.Printf("Note: namespace creation returned: %v\n", err)
			}

			// Cleanup on completion
			DeferCleanup(func() {
				fmt.Println("Cleaning up nodepool-default-test namespace...")
				_ = clientset.CoreV1().Namespaces().Delete(testContext, namespace, metav1.DeleteOptions{})
			})
		})

		It("THEN an application with no tolerations runs on a node with label container-platform.justice.gov.uk/default-ng=true", func() {
			podName = "nodepool-default-test-pod"

			// Create pod without any tolerations
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: namespace,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "test-container",
							Image: "busybox:latest",
							Command: []string{
								"sh", "-c",
								"sleep 3600",
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    *getResourceQuantity("100m"),
									corev1.ResourceMemory: *getResourceQuantity("64Mi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    *getResourceQuantity("200m"),
									corev1.ResourceMemory: *getResourceQuantity("128Mi"),
								},
							},
						},
					},
					// No tolerations specified
					RestartPolicy: corev1.RestartPolicyNever,
				},
			}

			// Create the pod
			deploymentObj, err := clientset.CoreV1().Pods(namespace).Create(testContext, pod, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			// Wait for pod to be running
			Eventually(func() error {
				pod, err := clientset.CoreV1().Pods(namespace).Get(testContext, podName, metav1.GetOptions{})
				if err != nil {
					return err
				}
				if pod.Status.Phase != corev1.PodRunning {
					return fmt.Errorf("pod phase is %s, waiting for Running", pod.Status.Phase)
				}
				return nil
			}, 60*time.Second, 5*time.Second).Should(Succeed())

			// Get the pod's node
			pod, err := clientset.CoreV1().Pods(namespace).Get(testContext, podName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())

			nodeName := pod.Spec.NodeName
			Expect(nodeName).NotTo(BeEmpty())
			fmt.Printf("Pod scheduled on node: %s\n", nodeName)

			// Get the node and verify it has the correct label
			node, err := clientset.CoreV1().Nodes().Get(testContext, nodeName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())

			// Check for the default-ng label
			labels := node.GetLabels()
			defaultNgLabel := labels["container-platform.justice.gov.uk/default-ng"]
			Expect(defaultNgLabel).To(Equal("true"), "Node should have label container-platform.justice.gov.uk/default-ng=true")

			fmt.Printf("SUCCESS: Pod with no tolerations is running on node with correct label\n")
			fmt.Printf("Node labels: %v\n", labels)

			// Cleanup the pod
			_ = clientset.CoreV1().Pods(namespace).Delete(testContext, podName, metav1.DeleteOptions{})
		})
	})
})

// Helper function to create resource quantities
func getResourceQuantity(value string) *corev1.Quantity {
	quantity, _ := corev1.ParseQuantity(value)
	return &quantity
}
