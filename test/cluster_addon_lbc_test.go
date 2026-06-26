package integration_tests

// Test to verify that an AWS Load Balancer is successfully provisioned when a LoadBalancer Service is created

import (
    . "github.com/onsi/ginkgo/v2"
    . "github.com/onsi/gomega"

    corev1 "k8s.io/api/core/v1"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/apimachinery/pkg/util/intstr"
)

// Setup Kuberntes config, client and annotate default namespace
var _ = BeforeSuite(func() {
    //ctx = context.Background()

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

var _ = Describe("AWS Load Balancer Controller", func() {
    It("should provision an AWS load balancer for a Service", func() {

        // Step 1: Create backing pod
        pod := &corev1.Pod{
            ObjectMeta: metav1.ObjectMeta{
                Name:      "lbc-test-pod",
                Namespace: "default",
                Labels: map[string]string{
                    "app": "lbc-test",
                },
            },
            Spec: corev1.PodSpec{
                Containers: []corev1.Container{
                    {
                        Name:  "pause",
                        Image: "public.ecr.aws/eks-distro/kubernetes/pause:3.9",
                    },
                },
            },
        }

        _, err := clientset.CoreV1().
            Pods("default").
            Create(ctx, pod, metav1.CreateOptions{})
        Expect(err).ToNot(HaveOccurred())
        // Pod cleanup
        DeferCleanup(func() {
            _ = clientset.CoreV1().
                Pods("default").
                Delete(ctx, "lbc-test-pod", metav1.DeleteOptions{})
        })

        // Step 2: Create service
        svc := &corev1.Service{
            ObjectMeta: metav1.ObjectMeta{
                Name:      "lbc-test",
                Namespace: "default",
                Annotations: map[string]string{
                    "service.beta.kubernetes.io/aws-load-balancer-type": "nlb",
                },
            },
            Spec: corev1.ServiceSpec{
                Selector: map[string]string{
                    "app": "lbc-test",
                },
                Ports: []corev1.ServicePort{
                    {
                        Port: 80,
                        TargetPort: intstr.FromInt(80),
                    },
                },
                Type: corev1.ServiceTypeLoadBalancer,
            },
        }    
        _, err = clientset.CoreV1().Services("default").Create(ctx, svc, metav1.CreateOptions{})
        Expect(err).ToNot(HaveOccurred())
        // Service Cleanup
        DeferCleanup(func() {
            _ = clientset.CoreV1().Services("default").
                Delete(ctx, "lbc-test", metav1.DeleteOptions{})
        })

        /* Test the Load Balancer. Includes:
            Retrieves and validates the Service "lbc-test" from the default namespace
            Verifies that the Service type is set to LoadBalancer
            Checks that the Service has the AWS annotation configured for an NLB
            Ensures that the Service defines at least one port
            Validates that a ClusterIP has been assigned to the Service
            Checks that a NodePort has been allocated for the Service when ports are present
            Verifies that the LoadBalancer ingress information has been populated
            Ensures that the LoadBalancer hostname is not empty when ingress exists
            Checks that the hostname matches the expected AWS ELB/NLB pattern (i.e. contains "elb")
            Retrieves the Endpoints associated with the Service
            Asserts that the Endpoints retrieval succeeded without errors
            Calculates the number of backend endpoint addresses
            Verifies that at least one endpoint is registered behind the Service
        */
        Eventually(func(g Gomega) {
            s, err := clientset.CoreV1().
                Services("default").
                Get(ctx, "lbc-test", metav1.GetOptions{})
        
            // Ensure we successfully retrieved the Service from the API
            g.Expect(err).ToNot(HaveOccurred())
            g.Expect(s).ToNot(BeNil())
        
            // Verify the Service is of type LoadBalancer
            g.Expect(s.Spec.Type).To(
                Equal(corev1.ServiceTypeLoadBalancer),
                "service should be of type LoadBalancer",
            )
        
            // Verify the AWS load balancer annotation is correctly set to NLB
            g.Expect(s.Annotations).To(
                HaveKeyWithValue(
                    "service.beta.kubernetes.io/aws-load-balancer-type", "nlb",
                ),
                "service should have NLB annotation",
            )
        
            // Verify the Service exposes at least one port
            g.Expect(s.Spec.Ports).ToNot(
                BeEmpty(),
                "service should define at least one port",
            )
        
            // Verify Kubernetes has assigned a ClusterIP
            g.Expect(s.Spec.ClusterIP).ToNot(
                BeEmpty(),
                "service should have a ClusterIP assigned",
            )
        
            // Verify NodePort has been allocated (required for NLB routing)
            if len(s.Spec.Ports) > 0 {
                g.Expect(s.Spec.Ports[0].NodePort).To(
                    BeNumerically(">", 0),
                    "service should have a NodePort allocated",
                )
            }
        
            // Verify the load balancer ingress entry has been created
            g.Expect(s.Status.LoadBalancer.Ingress).ToNot(
                BeEmpty(),
                "service should have a load balancer ingress entry",
            )
        
            // Validate the load balancer hostname looks like an AWS ELB/NLB hostname
            if len(s.Status.LoadBalancer.Ingress) > 0 {
                hostname := s.Status.LoadBalancer.Ingress[0].Hostname
                g.Expect(hostname).ToNot(
                    BeEmpty(),
                    "load balancer hostname should not be empty",
                )
                g.Expect(hostname).To(
                    ContainSubstring("elb"),
                    "hostname should look like an AWS ELB/NLB address",
                )
            }
        
            // Verify that endpoints exist, meaning the Service is correctly targeting pods
            ep, err := clientset.CoreV1().
                Endpoints("default").
                Get(ctx, "lbc-test", metav1.GetOptions{})
        
            g.Expect(err).ToNot(HaveOccurred())
        
            endpointCount := 0
            for _, subset := range ep.Subsets {
                endpointCount += len(subset.Addresses)
            }
        
            g.Expect(endpointCount).To(
                BeNumerically(">", 0),
                "service should have at least one backing endpoint",
            )
        
        }, "1m", "2s").Should(Succeed())
    })
})
