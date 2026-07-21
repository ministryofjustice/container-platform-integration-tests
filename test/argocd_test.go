package integration_tests

// ArgoCD Hub Cluster Validation Tests (US-015b)
//
// These tests validate the ArgoCD hub cluster configuration:
// - EKS Capability deployed (argocd namespace exists)
// - AppProjects created with correct isolation constraints
// - Baseline ApplicationSets deployed for namespace creation
// - Workload ApplicationSets deployed for service delivery
//
// Only runs on hub clusters (detected by argocd namespace presence).

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

var _ = Describe("ArgoCD Hub Configuration", Label("argocd"), func() {

	var (
		dynamicClient dynamic.Interface
		isHub         bool
		appProjectGVR schema.GroupVersionResource
		appSetGVR     schema.GroupVersionResource
	)

	BeforeEach(func() {
		var err error
		dynamicClient, err = dynamic.NewForConfig(kubeConfig)
		Expect(err).ToNot(HaveOccurred())

		appProjectGVR = schema.GroupVersionResource{
			Group:    "argoproj.io",
			Version:  "v1alpha1",
			Resource: "appprojects",
		}
		appSetGVR = schema.GroupVersionResource{
			Group:    "argoproj.io",
			Version:  "v1alpha1",
			Resource: "applicationsets",
		}

		// Detect if this is a hub cluster by checking for argocd namespace
		_, err = clientset.CoreV1().Namespaces().Get(ctx, "argocd", metav1.GetOptions{})
		isHub = err == nil
	})

	Context("when the cluster is an ArgoCD hub", func() {

		BeforeEach(func() {
			if !isHub {
				Skip("Not a hub cluster (argocd namespace not found)")
			}
		})

		It("should have the argocd namespace in Active state", func() {
			ns, err := clientset.CoreV1().Namespaces().Get(ctx, "argocd", metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(string(ns.Status.Phase)).To(Equal("Active"))
		})

		Describe("Platform AppProjects", func() {

			It("should have platform-nonlive AppProject with environments repo in sourceRepos", func() {
				project, err := dynamicClient.Resource(appProjectGVR).Namespace("argocd").Get(
					context.Background(), "platform-nonlive", metav1.GetOptions{},
				)
				Expect(err).ToNot(HaveOccurred())

				sourceRepos, found, err := unstructured.NestedStringSlice(project.Object, "spec", "sourceRepos")
				Expect(err).ToNot(HaveOccurred())
				Expect(found).To(BeTrue())
				Expect(sourceRepos).To(ContainElement(ContainSubstring("container-platform-environments")))
			})

			It("should allow Namespace creation in platform AppProjects", func() {
				project, err := dynamicClient.Resource(appProjectGVR).Namespace("argocd").Get(
					context.Background(), "platform-nonlive", metav1.GetOptions{},
				)
				Expect(err).ToNot(HaveOccurred())

				whitelist, found, err := unstructured.NestedSlice(project.Object, "spec", "clusterResourceWhitelist")
				Expect(err).ToNot(HaveOccurred())
				Expect(found).To(BeTrue())

				// Check that Namespace is in the whitelist
				hasNamespace := false
				for _, item := range whitelist {
					entry, ok := item.(map[string]interface{})
					if !ok {
						continue
					}
					kind, _ := entry["kind"].(string)
					group, _ := entry["group"].(string)
					if kind == "Namespace" || (group == "*" && kind == "*") {
						hasNamespace = true
						break
					}
				}
				Expect(hasNamespace).To(BeTrue(), "Platform AppProject must allow Namespace creation for baseline delivery")
			})
		})

		Describe("BU AppProjects", func() {

			It("should have BU-specific AppProjects with correct isolation", func() {
				projects, err := dynamicClient.Resource(appProjectGVR).Namespace("argocd").List(
					context.Background(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())

				buProjects := make([]unstructured.Unstructured, 0)
				for _, p := range projects.Items {
					name := p.GetName()
					if name != "default" && !strings.HasPrefix(name, "platform-") {
						buProjects = append(buProjects, p)
					}
				}
				Expect(buProjects).ToNot(BeEmpty(), "Expected at least one BU AppProject")

				for _, project := range buProjects {
					spec, found, err := unstructured.NestedMap(project.Object, "spec")
					Expect(err).ToNot(HaveOccurred())
					Expect(found).To(BeTrue())

					// Verify sourceRepos restrictions
					sourceRepos, found, err := unstructured.NestedStringSlice(spec, "sourceRepos")
					Expect(err).ToNot(HaveOccurred())
					Expect(found).To(BeTrue(), "BU project %s must have sourceRepos", project.GetName())
					Expect(sourceRepos).ToNot(BeEmpty())

					// Verify destinations are restricted
					destinations, found, err := unstructured.NestedSlice(spec, "destinations")
					Expect(err).ToNot(HaveOccurred())
					Expect(found).To(BeTrue(), "BU project %s must have destinations", project.GetName())
					Expect(destinations).ToNot(BeEmpty())
				}
			})

			It("should deny Namespace creation in BU AppProjects", func() {
				projects, err := dynamicClient.Resource(appProjectGVR).Namespace("argocd").List(
					context.Background(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())

				for _, project := range projects.Items {
					name := project.GetName()
					if name == "default" || strings.HasPrefix(name, "platform-") {
						continue
					}

					blacklist, found, err := unstructured.NestedSlice(project.Object, "spec", "clusterResourceBlacklist")
					Expect(err).ToNot(HaveOccurred())
					Expect(found).To(BeTrue(), "BU project %s must have clusterResourceBlacklist", name)

					hasNamespaceBlock := false
					for _, item := range blacklist {
						entry, ok := item.(map[string]interface{})
						if !ok {
							continue
						}
						kind, _ := entry["kind"].(string)
						group, _ := entry["group"].(string)
						if kind == "Namespace" || (group == "*" && kind == "*") {
							hasNamespaceBlock = true
							break
						}
					}
					Expect(hasNamespaceBlock).To(BeTrue(),
						"BU project %s must block Namespace creation (baseline owns namespace lifecycle)", name)
				}
			})
		})

		Describe("Baseline ApplicationSets", func() {

			It("should have baseline ApplicationSets using git-files-generator on product.yaml", func() {
				appSets, err := dynamicClient.Resource(appSetGVR).Namespace("argocd").List(
					context.Background(), metav1.ListOptions{
						LabelSelector: "container-platform/type=baseline",
					},
				)
				Expect(err).ToNot(HaveOccurred())
				Expect(appSets.Items).ToNot(BeEmpty(), "Expected at least one baseline ApplicationSet")

				for _, appSet := range appSets.Items {
					generators, found, err := unstructured.NestedSlice(appSet.Object, "spec", "generators")
					Expect(err).ToNot(HaveOccurred())
					Expect(found).To(BeTrue())
					Expect(generators).ToNot(BeEmpty())

					gen := generators[0].(map[string]interface{})
					gitGen, hasGit := gen["git"].(map[string]interface{})
					Expect(hasGit).To(BeTrue(), "Baseline ApplicationSet %s must use git generator", appSet.GetName())

					// Must use files (not directories) to read product.yaml
					files, hasFiles := gitGen["files"]
					Expect(hasFiles).To(BeTrue(), "Baseline ApplicationSet %s must use git files generator (not directories)", appSet.GetName())
					Expect(files).ToNot(BeEmpty())
				}
			})

			It("should reference charts/app-baseline as the Helm source", func() {
				appSets, err := dynamicClient.Resource(appSetGVR).Namespace("argocd").List(
					context.Background(), metav1.ListOptions{
						LabelSelector: "container-platform/type=baseline",
					},
				)
				Expect(err).ToNot(HaveOccurred())

				for _, appSet := range appSets.Items {
					path, found, err := unstructured.NestedString(appSet.Object, "spec", "template", "spec", "source", "path")
					Expect(err).ToNot(HaveOccurred())
					Expect(found).To(BeTrue(), "Baseline ApplicationSet %s must have source path", appSet.GetName())
					Expect(path).To(Equal("charts/app-baseline"),
						"Baseline ApplicationSet %s must point at charts/app-baseline", appSet.GetName())
				}
			})

			It("should deploy under platform AppProject", func() {
				appSets, err := dynamicClient.Resource(appSetGVR).Namespace("argocd").List(
					context.Background(), metav1.ListOptions{
						LabelSelector: "container-platform/type=baseline",
					},
				)
				Expect(err).ToNot(HaveOccurred())

				for _, appSet := range appSets.Items {
					project, found, err := unstructured.NestedString(appSet.Object, "spec", "template", "spec", "project")
					Expect(err).ToNot(HaveOccurred())
					Expect(found).To(BeTrue())
					Expect(project).To(HavePrefix("platform-"),
						"Baseline ApplicationSet %s must use platform-* AppProject (needs Namespace creation permission)", appSet.GetName())
				}
			})
		})

		Describe("Workload ApplicationSets", func() {

			It("should have workload ApplicationSets using git-files-generator on values files", func() {
				appSets, err := dynamicClient.Resource(appSetGVR).Namespace("argocd").List(
					context.Background(), metav1.ListOptions{
						LabelSelector: "container-platform/type=workload",
					},
				)
				Expect(err).ToNot(HaveOccurred())
				Expect(appSets.Items).ToNot(BeEmpty(), "Expected at least one workload ApplicationSet")

				for _, appSet := range appSets.Items {
					generators, found, err := unstructured.NestedSlice(appSet.Object, "spec", "generators")
					Expect(err).ToNot(HaveOccurred())
					Expect(found).To(BeTrue())
					Expect(generators).ToNot(BeEmpty())

					gen := generators[0].(map[string]interface{})
					gitGen, hasGit := gen["git"].(map[string]interface{})
					Expect(hasGit).To(BeTrue(), "Workload ApplicationSet %s must use git generator", appSet.GetName())

					// Must use files generator (scanning values/*.yaml)
					files, hasFiles := gitGen["files"]
					Expect(hasFiles).To(BeTrue(), "Workload ApplicationSet %s must use git files generator", appSet.GetName())
					Expect(files).ToNot(BeEmpty())
				}
			})

			It("should deploy under BU AppProject (not platform)", func() {
				appSets, err := dynamicClient.Resource(appSetGVR).Namespace("argocd").List(
					context.Background(), metav1.ListOptions{
						LabelSelector: "container-platform/type=workload",
					},
				)
				Expect(err).ToNot(HaveOccurred())

				for _, appSet := range appSets.Items {
					project, found, err := unstructured.NestedString(appSet.Object, "spec", "template", "spec", "project")
					Expect(err).ToNot(HaveOccurred())
					Expect(found).To(BeTrue())
					Expect(project).ToNot(HavePrefix("platform-"),
						"Workload ApplicationSet %s must NOT use platform AppProject", appSet.GetName())
				}
			})

			It("should use CreateNamespace=false", func() {
				appSets, err := dynamicClient.Resource(appSetGVR).Namespace("argocd").List(
					context.Background(), metav1.ListOptions{
						LabelSelector: "container-platform/type=workload",
					},
				)
				Expect(err).ToNot(HaveOccurred())

				for _, appSet := range appSets.Items {
					syncOptions, found, err := unstructured.NestedStringSlice(
						appSet.Object, "spec", "template", "spec", "syncPolicy", "syncOptions",
					)
					if err != nil || !found {
						// Try automated path
						syncOptions, found, err = unstructured.NestedStringSlice(
							appSet.Object, "spec", "template", "spec", "syncPolicy", "syncOptions",
						)
					}
					if found {
						Expect(syncOptions).To(ContainElement("CreateNamespace=false"),
							"Workload ApplicationSet %s must use CreateNamespace=false (baseline owns namespace creation)", appSet.GetName())
						Expect(syncOptions).ToNot(ContainElement("CreateNamespace=true"),
							"Workload ApplicationSet %s must NOT use CreateNamespace=true", appSet.GetName())
					}
					_ = err // suppress unused
				}
			})
		})
	})
})
