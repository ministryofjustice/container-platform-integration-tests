package helpers

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// nodeDiagnosticGVR is the EKS Node Monitoring Agent CRD that ships with EKS
// Auto Mode clusters. It provides on-demand, RBAC-controlled access to node
// logs without deploying any privileged or host-level workloads.
var nodeDiagnosticGVR = schema.GroupVersionResource{
	Group:    "eks.amazonaws.com",
	Version:  "v1alpha1",
	Resource: "nodediagnostics",
}

// CaptureNodeLogsE requests an on-demand node log bundle for nodeName via a
// NodeDiagnostic resource (destination "node") and downloads the resulting
// tarball through the kubelet node proxy API.
//
// NodeDiagnostic is the AWS-recommended way of retrieving logs from an EKS
// Auto Mode node:
//   - https://docs.aws.amazon.com/eks/latest/userguide/auto-troubleshoot.html
//   - https://docs.aws.amazon.com/eks/latest/userguide/auto-get-logs.html
//
// This removes the need for privileged log-tailer DaemonSets when validating
// node-level log output, e.g. the VPC CNI network policy agent log written
// when the NodeClass sets networkPolicyEventLogs: Enabled.
//
// The caller's identity requires RBAC: create/delete on
// nodediagnostics.eks.amazonaws.com and get on nodes/proxy.
func CaptureNodeLogsE(ctx context.Context, config *rest.Config, nodeName string) ([]byte, error) {
	dynClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create dynamic client: %w", err)
	}

	diagnostic := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "eks.amazonaws.com/v1alpha1",
		"kind":       "NodeDiagnostic",
		"metadata":   map[string]interface{}{"name": nodeName},
		"spec": map[string]interface{}{
			"logCapture": map[string]interface{}{
				// Networking is the narrowest category that bundles the VPC
				// CNI logs under /var/log/aws-routed-eni/, including
				// network-policy-agent.log. Avoid "All", which pulls the
				// entire node journal onto the caller's machine.
				"categories":  []interface{}{"Networking"},
				"destination": "node",
			},
		},
	}}

	if _, err := dynClient.Resource(nodeDiagnosticGVR).Create(ctx, diagnostic, metav1.CreateOptions{}); err != nil {
		return nil, fmt.Errorf("create NodeDiagnostic for node %q: %w", nodeName, err)
	}

	// The NodeDiagnostic is single-use; delete it once the bundle is fetched.
	defer func() {
		_ = dynClient.Resource(nodeDiagnosticGVR).Delete(context.Background(), nodeName, metav1.DeleteOptions{})
	}()

	err = wait.PollUntilContextTimeout(ctx, 5*time.Second, 3*time.Minute, true, func(ctx context.Context) (bool, error) {
		current, err := dynClient.Resource(nodeDiagnosticGVR).Get(ctx, nodeName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		statuses, found, err := unstructured.NestedSlice(current.Object, "status", "captureStatuses")
		if err != nil || !found {
			return false, err
		}

		for _, s := range statuses {
			status, ok := s.(map[string]interface{})
			if !ok {
				continue
			}
			completed, found, err := unstructured.NestedMap(status, "state", "completed")
			if err != nil || !found {
				continue
			}
			reason, _, _ := unstructured.NestedString(completed, "reason")
			message, _, _ := unstructured.NestedString(completed, "message")
			if reason == "Success" || reason == "SuccessWithErrors" {
				return true, nil
			}
			return false, fmt.Errorf("NodeDiagnostic log capture failed on node %q: reason=%s message=%s", nodeName, reason, message)
		}
		return false, nil
	})
	if err != nil {
		return nil, fmt.Errorf("waiting for NodeDiagnostic on node %q: %w", nodeName, err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create clientset: %w", err)
	}

	// The node monitoring agent writes the bundle to /var/log/support/ on the
	// node; the kubelet serves it via the node proxy API.
	bundleName := fmt.Sprintf("%s-logs.tar.gz", nodeName)
	tarball, err := clientset.CoreV1().RESTClient().Get().
		Resource("nodes").
		Name(nodeName).
		SubResource("proxy").
		Suffix("logs", "support", bundleName).
		DoRaw(ctx)
	if err != nil {
		return nil, fmt.Errorf("download %q via node proxy: %w", bundleName, err)
	}

	return tarball, nil
}

// ExtractFromTarGzE returns the contents of the first regular file in a
// gzipped tar archive whose name ends with fileName, so
// "network-policy-agent.log" matches
// "var_log/aws-routed-eni/network-policy-agent.log".
func ExtractFromTarGzE(archive []byte, fileName string) (string, error) {
	gzReader, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return "", fmt.Errorf("open gzip archive: %w", err)
	}
	defer gzReader.Close()

	tarReader := tar.NewReader(gzReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read tar archive: %w", err)
		}
		if header.Typeflag == tar.TypeReg && strings.HasSuffix(header.Name, fileName) {
			contents, err := io.ReadAll(tarReader)
			if err != nil {
				return "", fmt.Errorf("read %q from archive: %w", fileName, err)
			}
			return string(contents), nil
		}
	}

	return "", fmt.Errorf("file %q not found in archive", fileName)
}
