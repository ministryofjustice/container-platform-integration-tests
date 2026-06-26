package integration_tests

import (
    "context"
    "fmt"
    "time"

    . "github.com/onsi/ginkgo/v2"
    . "github.com/onsi/gomega"

    corev1 "k8s.io/api/core/v1"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/apimachinery/pkg/api/resource"

    "k8s.io/client-go/kubernetes"
    "k8s.io/client-go/tools/clientcmd"

    storageclient "k8s.io/client-go/kubernetes/typed/storage/v1"
)

var (
    clientset    *kubernetes.Clientset
    storageClient *storageclient.StorageV1Client
)

var _ = BeforeSuite(func() {
    //ctx := context.Background()

    kubeconfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
        clientcmd.NewDefaultClientConfigLoadingRules(),
        &clientcmd.ConfigOverrides{},
    )

    config, err := kubeconfig.ClientConfig()
    Expect(err).ToNot(HaveOccurred())

    clientset, err = kubernetes.NewForConfig(config)
    Expect(err).ToNot(HaveOccurred())

    storageClient, err = storageclient.NewForConfig(config)
    Expect(err).ToNot(HaveOccurred())
})

var _ = Describe("EKS Auto Mode", func() {

    It("should dynamically provision an EBS volume", func() {
        ctx := context.Background()
        ebsTestName := fmt.Sprintf("ebs-test-%d", GinkgoParallelProcess())
    
        // Step 1: Create PVC with explicit storage class
        pvc := &corev1.PersistentVolumeClaim{
            ObjectMeta: metav1.ObjectMeta{
                Name:      ebsTestName,
                Namespace: "default",
            },
            Spec: corev1.PersistentVolumeClaimSpec{
//                StorageClassName: ptr("gp2"), // removed now we have a default storage class
                AccessModes: []corev1.PersistentVolumeAccessMode{
                    corev1.ReadWriteOnce,
                },
                Resources: corev1.VolumeResourceRequirements{
                    Requests: corev1.ResourceList{
                        corev1.ResourceStorage: resource.MustParse("1Gi"),
                    },
                },
            },
        }
    
        _, err := clientset.CoreV1().
            PersistentVolumeClaims("default").
            Create(ctx, pvc, metav1.CreateOptions{})
        Expect(err).ToNot(HaveOccurred())
    
        DeferCleanup(func() {
            _ = clientset.CoreV1().
                PersistentVolumeClaims("default").
                Delete(ctx, ebsTestName, metav1.DeleteOptions{})
        })

        // Step 2: Create Pod that uses the PVC
        pod := &corev1.Pod{
            ObjectMeta: metav1.ObjectMeta{
                Name:      ebsTestName,
                Namespace: "default",
            },
            Spec: corev1.PodSpec{
                Containers: []corev1.Container{
                    {
                        Name:    "app",
                        Image:   "busybox",
                        Command: []string{"sleep", "3600"},
                        VolumeMounts: []corev1.VolumeMount{
                            {
                                Name:      "data",
                                MountPath: "/data",
                            },
                        },
                    },
                },
                Volumes: []corev1.Volume{
                    {
                        Name: "data",
                        VolumeSource: corev1.VolumeSource{
                            PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
                                ClaimName: ebsTestName,
                            },
                        },
                    },
                },
            },
        }

        _, err = clientset.CoreV1().
            Pods("default").
            Create(ctx, pod, metav1.CreateOptions{})
        Expect(err).ToNot(HaveOccurred())
    
        DeferCleanup(func() {
            _ = clientset.CoreV1().
                Pods("default").
                Delete(ctx, ebsTestName, metav1.DeleteOptions{})
        })

        // Step 3: Wait for PVC to bind
        Eventually(func() string {
            p, _ := clientset.CoreV1().
                PersistentVolumeClaims("default").
                Get(ctx, ebsTestName, metav1.GetOptions{})
    
            return string(p.Status.Phase)
        }, "10m", "5s").Should(Equal("Bound")) // Longest Reported Test Time 5m10s

        time.Sleep(5 * time.Minute)

    })

})
