package integration_tests

import (
	"fmt"
	"html/template"
	"strconv"
	"strings"

	"github.com/gruntwork-io/terratest/modules/k8s"
	"github.com/gruntwork-io/terratest/modules/logger"
	"github.com/gruntwork-io/terratest/modules/random"
	"github.com/ministryofjustice/container-platform-integration-tests/test/helpers"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("pod-to-pod communication", Serial, Ordered, func() {
	var (

		// Create namespaces, pods and services using iterable structures
		namespaceA string
		namespaceB string

		optionsA *k8s.KubectlOptions
		optionsB *k8s.KubectlOptions

		oldLogger *logger.Logger

		pods []struct {
			name      string
			namespace string
			options   *k8s.KubectlOptions
		}

		namespaces []struct {
			name    string
			options *k8s.KubectlOptions
		}

		services []struct {
			name      string
			namespace string
			podName   string
			options   *k8s.KubectlOptions
		}

		// Unique per-run name for the netlog DaemonSet, e.g. netlog-ab12cd
		netlogName string

		// The netlog DaemonSet must run in "default" (see BeforeAll comment)
		netlogOptions *k8s.KubectlOptions
	)

	BeforeAll(func() {
		GinkgoWriter.Println("STEP -1: Deleting any orphaned netlog DaemonSets from previous runs")

		// The cluster's Gatekeeper policy (user-ns-require-psa-label) forces
		// every namespace we create to enforce the "restricted" PSA level with
		// no override allowed. The netlog DaemonSet requires hostPath/hostNetwork/
		// "default" namespace, which is exempt from that constraint.
		defaultOptions := k8s.NewKubectlOptions("", "", "default")
		netlogOptions = defaultOptions
		Expect(deleteManagedNetlogDaemonSets()).To(Succeed())

		// Generate unique namespace names for the test
		namespaceA = fmt.Sprintf(
			"%s-pod2pod-namespace-a-%s",
			c.Prefix,
			strings.ToLower(random.UniqueId()),
		)
		namespaceB = fmt.Sprintf(
			"%s-pod2pod-namespace-b-%s",
			c.Prefix,
			strings.ToLower(random.UniqueId()),
		)
		// Create KubectlOptions for each namespace

		optionsA = k8s.NewKubectlOptions("", "", namespaceA)
		optionsB = k8s.NewKubectlOptions("", "", namespaceB)
		// Store the original logger and set the default logger for both options
		oldLogger = optionsA.Logger
		optionsA.Logger = logger.Default
		optionsB.Logger = logger.Default

		namespaces = []struct {
			name    string
			options *k8s.KubectlOptions
		}{
			{namespaceA, optionsA},
			{namespaceB, optionsB},
		}
		// Define pods and services for the test 2 pods in namespaceA and 1 pod in namespaceB, each with a corresponding service

		pods = []struct {
			name      string
			namespace string
			options   *k8s.KubectlOptions
		}{
			{
				name:      "pod-a",
				namespace: namespaceA,
				options:   optionsA,
			},
			{
				name:      "pod-b",
				namespace: namespaceA,
				options:   optionsA,
			},
			{
				name:      "pod-c",
				namespace: namespaceB,
				options:   optionsB,
			},
		}

		services = []struct {
			name      string
			namespace string
			podName   string
			options   *k8s.KubectlOptions
		}{
			{
				name:      "service-a",
				namespace: namespaceA,
				podName:   "pod-a",
				options:   optionsA,
			},
			{
				name:      "service-b",
				namespace: namespaceA,
				podName:   "pod-b",
				options:   optionsA,
			},
			{
				name:      "service-c",
				namespace: namespaceB,
				podName:   "pod-c",
				options:   optionsB,
			},
		}

		GinkgoWriter.Println("STEP 0: Create namespaces")

		for _, ns := range namespaces {

			GinkgoWriter.Printf("Rendering namespace template for %s\n", ns.name)

			nsTpl, err := helpers.TemplateFile(
				"./fixtures/namespace.yaml.tmpl",
				"namespace.yaml.tmpl",
				template.FuncMap{
					"namespace": ns.name,
				},
			)
			Expect(err).NotTo(HaveOccurred())

			GinkgoWriter.Printf("Applying namespace for %s\n", ns.name)

			err = k8s.KubectlApplyFromStringE(
				GinkgoT(),
				ns.options,
				nsTpl,
			)
			Expect(err).NotTo(HaveOccurred())

			_, err = k8s.RunKubectlAndGetOutputE(
				GinkgoT(),
				ns.options,
				"label",
				"namespace",
				ns.name,
				fmt.Sprintf("name=%s", ns.name),
			)

			Expect(err).NotTo(HaveOccurred())
		}

		GinkgoWriter.Println("STEP 1: Deploying netlog DaemonSet across nodes")
		// Deploy the netlog DaemonSet into "default" - required because it needs
		// hostPath/hostNetwork/hostPID/privileged access to read node-level
		// network policy logs, which the cluster's Gatekeeper policy forbids in
		// any namespace we create (see note in BeforeAll above)

		// Every run gets its own uniquely-named DaemonSet, e.g. netlog-ab12cd
		netlogName = fmt.Sprintf("netlog-%s", strings.ToLower(random.UniqueId()))

		netlogTpl, err := helpers.TemplateFile(
			"./fixtures/netlog-daemonset.yaml.tmpl",
			"netlog-daemonset.yaml.tmpl",
			template.FuncMap{
				"name":      netlogName,
				"namespace": "default",
			},
		)
		Expect(err).NotTo(HaveOccurred())

		err = k8s.KubectlApplyFromStringE(
			GinkgoT(),
			defaultOptions,
			netlogTpl,
		)
		Expect(err).NotTo(HaveOccurred())

		// Wait for netlog DaemonSet to be scheduled and ready across nodes
		Eventually(func() error {
			output, err := k8s.RunKubectlAndGetOutputE(
				GinkgoT(),
				defaultOptions,
				"get", "daemonset", netlogName,
				"-o", "jsonpath={.status.numberReady}",
			)
			if err != nil {
				return err
			}
			if output == "0" || output == "" {
				return fmt.Errorf("netlog daemonset has 0 ready pods")
			}
			return nil
		}, "1m", "5s").Should(Succeed())

		GinkgoWriter.Println("STEP 2: rendering pod template")

		for _, pod := range pods {

			GinkgoWriter.Printf("Rendering pod template for %s in namespace %s\n", pod.name, pod.namespace)

			podTpl, err := helpers.TemplateFile(
				"./fixtures/networkpolicy-test-pod-curl.yaml.tmpl",
				"networkpolicy-test-pod-curl.yaml.tmpl",
				template.FuncMap{
					"namespace": pod.namespace,
					"podName":   pod.name,
				},
			)
			Expect(err).NotTo(HaveOccurred())

			GinkgoWriter.Printf("Applying pod for %s in namespace %s\n", pod.name, pod.namespace)

			err = k8s.KubectlApplyFromStringE(
				GinkgoT(),
				pod.options,
				podTpl,
			)
			Expect(err).NotTo(HaveOccurred())
		}

		GinkgoWriter.Println("STEP 3: waiting for pod readiness")

		for _, pod := range pods {
			Eventually(func() error {
				output, err := k8s.RunKubectlAndGetOutputE(
					GinkgoT(),
					pod.options,
					"get",
					"pod",
					pod.name,
					"-o",
					"jsonpath={.status.conditions[?(@.type=='Ready')].status}",
				)

				if err != nil {
					return err
				}

				if output != "True" {
					return fmt.Errorf("%s not ready", pod.name)
				}

				return nil
			}, "2m", "5s").Should(Succeed())
		}

		GinkgoWriter.Println("STEP 4: Create services for pods")

		for _, service := range services {

			GinkgoWriter.Printf(
				"Rendering service %s\n",
				service.name,
			)

			serviceTpl, err := helpers.TemplateFile(
				"./fixtures/service.yaml.tmpl",
				"service.yaml.tmpl",
				template.FuncMap{
					"serviceName": service.name,
					"namespace":   service.namespace,
					"podName":     service.podName,
				},
			)

			Expect(err).NotTo(HaveOccurred())

			err = k8s.KubectlApplyFromStringE(
				GinkgoT(),
				service.options,
				serviceTpl,
			)

			Expect(err).NotTo(HaveOccurred())
		}

		GinkgoWriter.Println("STEP 5: waiting for service endpoints")

		for _, service := range services {

			Eventually(func() error {

				output, err := k8s.RunKubectlAndGetOutputE(
					GinkgoT(),
					service.options,
					"get",
					"endpoints",
					service.name,
					"-o",
					"jsonpath={.subsets[*].addresses[*].ip}",
				)

				if err != nil {
					return err
				}

				if output == "" {
					return fmt.Errorf("service %s has no endpoints", service.name)
				}

				return nil

			}, "2m", "5s").Should(Succeed())
		}
	})

	Context("without policies", func() {

		It("allows pod-a to reach service-b in the same namespace within 2ms added latency", func() {
			Eventually(func() error {
				// Execute curl and output both the HTTP response body and timing info
				// %{time_connect} time, in seconds it took from start until the TCP connect to the remote host (or proxy) was completed.
				// %{time_total} The total time, in seconds, that the full operation lasted. The time will be displayed with millisecond resolution.
				output, err := k8s.RunKubectlAndGetOutputE(
					GinkgoT(),
					optionsA,
					"exec",
					"pod-a",
					"--",
					"curl",
					"-s",
					"-w", "\nTIME_CONNECT:%{time_connect}\nTIME_TOTAL:%{time_total}",
					"http://service-b:8080",
				)
				if err != nil {
					return err
				}

				// 1. Verify expected response body
				if !strings.Contains(output, "Hello from pod-b") {
					return fmt.Errorf("unexpected response body: %q", output)
				}

				// 2. Extract and parse latency metrics
				var connectTimeSeconds float64
				for _, line := range strings.Split(output, "\n") {
					if strings.HasPrefix(line, "TIME_CONNECT:") {
						//Finds and Stores tcp connection time as valStr
						valStr := strings.TrimPrefix(line, "TIME_CONNECT:")
						// Convert string to float
						parsedVal, parseErr := strconv.ParseFloat(strings.TrimSpace(valStr), 64)
						if parseErr != nil {
							return fmt.Errorf("failed to parse connection time %q: %w", valStr, parseErr)
						}
						connectTimeSeconds = parsedVal
						break
					}
				}

				// Convert seconds to milliseconds
				latencyMs := connectTimeSeconds * 1000
				GinkgoWriter.Printf("Measured TCP connection latency: %.3f ms\n", latencyMs)

				// 3. Return error if latency exceeds 2ms
				if latencyMs >= 2.0 {
					return fmt.Errorf("latency too high: got %.3f ms (expected < 2ms)", latencyMs)
				}

				return nil
			}, "30s", "2s").Should(Succeed())
		})

	})

	Context("with default deny network policy", func() {
		BeforeEach(func() {
			for _, ns := range namespaces {
				GinkgoWriter.Printf(
					"Rendering default deny policy for %s\n",
					ns.name,
				)

				err := applyDefaultDenyPolicy(ns.name, ns.options)
				Expect(err).NotTo(HaveOccurred())

			}

		})

		It("blocks pod-a reaching service-c by default", func() {
			// Store the node name of pod-c to check netlog later

			destNode, err := getPodNodeName(optionsB, "pod-c")
			Expect(err).NotTo(HaveOccurred(), "Failed to get target node for pod-c")
			GinkgoWriter.Printf("Target pod-c is running on node: %s\n", destNode)

			// Capture the source (pod-a) and destination (pod-c) IPs so the deny
			// log can be matched to this specific flow rather than any DENY entry
			srcIP, err := getPodIP(optionsA, "pod-a")
			Expect(err).NotTo(HaveOccurred(), "Failed to get pod-a IP")
			GinkgoWriter.Printf("Source pod-a IP: %s\n", srcIP)

			dstIP, err := getPodIP(optionsB, "pod-c")
			Expect(err).NotTo(HaveOccurred(), "Failed to get pod-c IP")
			GinkgoWriter.Printf("Destination pod-c IP: %s\n", dstIP)

			Eventually(func() error {
				// Using curl with --connect-timeout 2 --max-time 3 so dropped traffic fails fast
				_, err := k8s.RunKubectlAndGetOutputE(
					GinkgoT(),
					optionsA,
					"exec",
					"pod-a",
					"--",
					"curl",
					"-s",
					"--connect-timeout", "2",
					"--max-time", "3",
					"http://service-c."+namespaceB+".svc.cluster.local:8080",
				)

				if err == nil {
					return fmt.Errorf("expected request to be blocked")
				}

				return nil
			}, "30s", "2s").Should(Succeed())

			Eventually(func() error {
				// Netlog DaemonSet pods live in "default" (see BeforeAll comment)
				logs, err := getNetlogForNode(netlogOptions, destNode)
				if err != nil {
					return err
				}

				// Narrow down to only the DENY log lines that also reference the
				// NETWORK_POLICY tier, then confirm at least one of those lines
				// matches this specific flow's source (pod-a) and destination (pod-c) IPs
				var matched bool
				for _, line := range strings.Split(logs, "\n") {
					if !strings.Contains(line, "Verdict DENY") || !strings.Contains(line, "NETWORK_POLICY") {
						continue
					}

					if strings.Contains(line, srcIP) && strings.Contains(line, dstIP) {
						matched = true
						break
					}
				}

				if !matched {
					return fmt.Errorf(
						"expected 'Verdict DENY' entry for flow %s -> %s in netlog on node %s, got:\n%s",
						srcIP, dstIP, destNode, logs,
					)
				}

				GinkgoWriter.Printf("Successfully verified Verdict DENY for flow %s -> %s in netlog logs!\n", srcIP, dstIP)
				return nil
			}, "45s", "2s").Should(Succeed())

		})
	})

	Context("with allow cross-namespace network policy", func() {

		BeforeEach(func() {
			Expect(applyDefaultDenyPolicy(namespaceA, optionsA)).To(Succeed())
			Expect(applyDefaultDenyPolicy(namespaceB, optionsB)).To(Succeed())
			Expect(applyAllowCrossNamespacePolicyLabel(namespaceA, namespaceB, optionsA)).To(Succeed())
			Expect(applyAllowCrossNamespacePolicyLabel(namespaceB, namespaceA, optionsB)).To(Succeed())
		})

		It("allows pod-a reaching service-c after allow policy is applied", func() {
			Eventually(func() error {
				output, err := k8s.RunKubectlAndGetOutputE(
					GinkgoT(),
					optionsA,
					"exec",
					"pod-a",
					"--",
					"curl",
					"-s",
					"http://service-c."+namespaceB+".svc.cluster.local:8080",
				)
				if err != nil {
					return err
				}

				if !strings.Contains(output, "Hello from pod-c") {
					return fmt.Errorf("unexpected response: %q", output)
				}

				return nil
			}, "30s", "2s").Should(Succeed())
		})

		It("blocks traffic when the namespace label is removed", func() {
			serviceFQDN := fmt.Sprintf("service-c.%s.svc.cluster.local:8080", namespaceB)

			// 1. Remove the 'name' label from namespaceA
			GinkgoWriter.Println("Removing 'name' label from namespace A...")
			_, err := k8s.RunKubectlAndGetOutputE(
				GinkgoT(),
				optionsA,
				"label", "namespace", namespaceA, "name-",
			)
			Expect(err).NotTo(HaveOccurred(), "Failed to remove 'name' label from namespace A")

			// 2. Confirm traffic gets blocked (connection fails/times out)
			GinkgoWriter.Println("Verifying cross-namespace traffic is now blocked...")
			Eventually(func() error {
				_, err := k8s.RunKubectlAndGetOutputE(
					GinkgoT(),
					optionsA,
					"exec", "pod-a", "--",
					"curl", "-s", "--connect-timeout", "2", "--max-time", "3",
					"http://"+serviceFQDN,
				)

				if err == nil {
					return fmt.Errorf("expected traffic to be blocked after removing 'name' label, but it succeeded")
				}

				return nil
			}, "15s", "2s").Should(Succeed(), "Traffic was still allowed without the matching namespace label")

			// 3. Restore the label so namespace deletion cleanup works smoothly
			GinkgoWriter.Println("Restoring 'name' label to namespace A...")
			_, _ = k8s.RunKubectlAndGetOutputE(
				GinkgoT(),
				optionsA,
				"label", "namespace", namespaceA, fmt.Sprintf("name=%s", namespaceA),
			)
		})
	})

	AfterAll(func() {
		defer func() {
			optionsA.Logger = oldLogger
			optionsB.Logger = oldLogger
		}()

		// The netlog DaemonSet lives in "default", so it must be deleted
		// explicitly - it is not covered by the test namespace deletions below.
		if netlogName != "" {
			_, err := k8s.RunKubectlAndGetOutputE(
				GinkgoT(),
				netlogOptions,
				"delete", "daemonset", netlogName,
				"--ignore-not-found=true",
			)
			Expect(err).NotTo(HaveOccurred())
		}

		Expect(k8s.DeleteNamespaceE(GinkgoT(), optionsA, namespaceA)).To(Succeed())
		Expect(k8s.DeleteNamespaceE(GinkgoT(), optionsB, namespaceB)).To(Succeed())

	})
})

func applyDefaultDenyPolicy(namespace string, options *k8s.KubectlOptions) error {
	policyTpl, err := helpers.TemplateFile(
		"./fixtures/default-deny-networkpolicy.yaml.tmpl",
		"default-deny-networkpolicy.yaml.tmpl",
		template.FuncMap{
			"NetworkPolicyName": "default-deny",
			"Namespace":         namespace,
		},
	)
	if err != nil {
		return err
	}

	return k8s.KubectlApplyFromStringE(
		GinkgoT(),
		options,
		policyTpl,
	)
}

func applyAllowCrossNamespacePolicyLabel(namespace, allowedNamespace string, options *k8s.KubectlOptions) error {
	policyTpl, err := helpers.TemplateFile(
		"./fixtures/networkpolicy.yaml.tmpl",
		"networkpolicy.yaml.tmpl",
		template.FuncMap{
			"name":                    "allow-cross-namespace-label",
			"namespace":               namespace,
			"allowSameNamespace":      false,
			"allowedNamespaces":       []string{allowedNamespace},
			"allowedEgressNamespaces": []string{allowedNamespace},
			"allowDNS":                true,
		},
	)
	if err != nil {
		return err
	}

	return k8s.KubectlApplyFromStringE(
		GinkgoT(),
		options,
		policyTpl,
	)
}

// deleteManagedNetlogDaemonSets deletes any netlog DaemonSets left behind by a
// previous run (identified via the managed-by label) and waits for the deletions
// to complete before returning, so a fresh DaemonSet can safely be created afterwards.
// The netlog DaemonSet lives in "default", but --all-namespaces is used here as a
// safety net in case a leftover ends up elsewhere.
func deleteManagedNetlogDaemonSets() error {
	// This command isn't scoped to a single namespace, so the namespace on
	// these options is irrelevant - only used to obtain kubeconfig context
	options := k8s.NewKubectlOptions("", "", "")
	const labelSelector = "managed-by=container-platform-integration-tests"

	_, err := k8s.RunKubectlAndGetOutputE(
		GinkgoT(),
		options,
		"delete", "daemonset",
		"--all-namespaces",
		"-l", labelSelector,
		"--ignore-not-found=true",
	)
	if err != nil {
		return err
	}

	// Wait for the deletions to complete before proceeding
	Eventually(func() error {
		output, err := k8s.RunKubectlAndGetOutputE(
			GinkgoT(),
			options,
			"get", "daemonsets",
			"--all-namespaces",
			"-l", labelSelector,
			"-o", "jsonpath={.items[*].metadata.name}",
		)
		if err != nil {
			return err
		}

		if strings.TrimSpace(output) != "" {
			return fmt.Errorf("daemonsets still terminating: %s", output)
		}

		return nil
	}, "1m", "2s").Should(Succeed())

	return nil
}

// getPodNodeName finds which EC2 node a target pod is running on
func getPodNodeName(options *k8s.KubectlOptions, podName string) (string, error) {
	return k8s.RunKubectlAndGetOutputE(
		GinkgoT(),
		options,
		"get", "pod", podName,
		"-o", "jsonpath={.spec.nodeName}",
	)
}

// getPodIP returns the pod IP address for a given pod, used to match deny logs to a specific flow
func getPodIP(options *k8s.KubectlOptions, podName string) (string, error) {
	return k8s.RunKubectlAndGetOutputE(
		GinkgoT(),
		options,
		"get", "pod", podName,
		"-o", "jsonpath={.status.podIP}",
	)
}

// getNetlogForNode fetches stdout logs from the netlog DaemonSet pod on a specific node.
// The netlog DaemonSet lives in "default". Uses --since (rather than a fixed
// --tail count) so busy nodes with lots of concurrent traffic don't scroll the
// target DENY event out of the captured window before it's found.
func getNetlogForNode(options *k8s.KubectlOptions, nodeName string) (string, error) {
	// 1. Get the netlog pod name running on the target node
	netlogPod, err := k8s.RunKubectlAndGetOutputE(
		GinkgoT(),
		options,
		"get", "pod",
		"-l", "app=netlog",
		"--field-selector", fmt.Sprintf("spec.nodeName=%s", nodeName),
		"-o", "jsonpath={.items[0].metadata.name}",
	)
	if err != nil || netlogPod == "" {
		return "", fmt.Errorf("failed to find netlog pod on node %s: %w", nodeName, err)
	}

	// 2. Fetch all log lines from the last 40s from that netlog pod.
	// Use a Discard logger for this call only, since terratest streams every
	// line of "kubectl logs" output to the logger as it's read - the netlog
	// DaemonSet is extremely chatty (debug-level ACCEPT flow logs for all
	// traffic on the node), so without this the test output is dominated by
	// noise unrelated to the DENY verdict being asserted on.
	quietOptions := *options
	quietOptions.Logger = logger.Discard

	logs, err := k8s.RunKubectlAndGetOutputE(
		GinkgoT(),
		&quietOptions,
		"logs", netlogPod,
		"--since=1m",
	)
	if err != nil {
		return "", err
	}

	// Print only the "Verdict DENY" lines so failures/successes are easy to
	// read, rather than dumping the whole (noisy) log output.
	var denyLines []string
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, "Verdict DENY") {
			denyLines = append(denyLines, line)
		}
	}
	if len(denyLines) > 0 {
		GinkgoWriter.Printf("Filtered 'Verdict DENY' lines from netlog on node %s:\n%s\n", nodeName, strings.Join(denyLines, "\n"))
	} else {
		GinkgoWriter.Printf("No 'Verdict DENY' lines found yet in netlog on node %s\n", nodeName)
	}

	return logs, nil
}
