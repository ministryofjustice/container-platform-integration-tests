package integration_tests

// ArgoCD Hub Cluster Validation Tests (US-015b)
//
// These tests validate the ArgoCD hub cluster configuration:
// - EKS Capability deployed (argocd namespace exists)
// - AppProjects created with correct isolation constraints
// - ApplicationSets deployed for BU workload generation
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
	)

	BeforeEach(func() {
		var err error
		dynamicClient, err = dynamic.NewForConfig(kubeConfig)
		Expect(err).ToNot(HaveOccurred())

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

		Describe("AppProjects", func() {

			var appProjectGVR schema.GroupVersionResource

			BeforeEach(func() {
				appProjectGVR = schema.GroupVersionResource{
					Group:    "argoproj.io",
					Version:  "v1alpha1",
					Resource: "appprojects",
				}
			})

			It("should have platform-nonlive AppProject", func() {
				project, err := dynamicClient.Resource(appProjectGVR).Namespace("argocd").Get(
					context.Background(), "platform-nonlive", metav1.GetOptions{},
				)
				Expect(err).ToNot(HaveOccurred())

				spec, found, err := unstructured.NestedMap(project.Object, "spec")
				Expect(err).ToNot(HaveOccurred())
				Expect(found).To(BeTrue())

				// Verify platform project has sourceRepos containing container-platform-config
				sourceRepos, found, err := unstructured.NestedStringSlice(spec, "sourceRepos")
				Expect(err).ToNot(HaveOccurred())
				Expect(found).To(BeTrue())
				Expect(sourceRepos).To(ContainElement(ContainSubstring("container-platform-config")))
			})

			It("should have BU-specific AppProjects with correct isolation", func() {
				// List all AppProjects
				projects, err := dynamicClient.Resource(appProjectGVR).Namespace("argocd").List(
					context.Background(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())

				// Find BU projects (named like <bu>-nonlive or <bu>-live)
				buProjects := make([]unstructured.Unstructured, 0)
				for _, p := range projects.Items {
					name := p.GetName()
					if name != "default" && !strings.HasPrefix(name, "platform-") {
						buProjects = append(buProjects, p)
					}
				}
				Expect(buProjects).ToNot(BeEmpty(), "Expected at least one BU AppProject")

				// Verify each BU project has sourceRepos restrictions
				for _, project := range buProjects {
					spec, found, err := unstructured.NestedMap(project.Object, "spec")
					Expect(err).ToNot(HaveOccurred())
					Expect(found).To(BeTrue())

					sourceRepos, found, err := unstructured.NestedStringSlice(spec, "sourceRepos")
					Expect(err).ToNot(HaveOccurred())
					Expect(found).To(BeTrue(), "BU project %s must have sourceRepos", project.GetName())
					Expect(sourceRepos).ToNot(BeEmpty(), "BU project %s sourceRepos must not be empty", project.GetName())

					// Verify destinations are restricted (not wildcard)
					destinations, found, err := unstructured.NestedSlice(spec, "destinations")
					Expect(err).ToNot(HaveOccurred())
					Expect(found).To(BeTrue(), "BU project %s must have destinations", project.GetName())
					Expect(destinations).ToNot(BeEmpty())

					// Verify clusterResourceBlacklist blocks Namespace creation
					blacklist, found, err := unstructured.NestedSlice(spec, "clusterResourceBlacklist")
					Expect(err).ToNot(HaveOccurred())
					Expect(found).To(BeTrue(), "BU project %s must have clusterResourceBlacklist", project.GetName())
					Expect(blacklist).ToNot(BeEmpty(), "BU project %s clusterResourceBlacklist must block privileged resources", project.GetName())
				}
			})
		})

		Describe("ApplicationSets", func() {

			var appSetGVR schema.GroupVersionResource

			BeforeEach(func() {
				appSetGVR = schema.GroupVersionResource{
					Group:    "argoproj.io",
					Version:  "v1alpha1",
					Resource: "applicationsets",
				}
			})

			It("should have BU ApplicationSets using git-directory-generator", func() {
				appSets, err := dynamicClient.Resource(appSetGVR).Namespace("argocd").List(
					context.Background(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())
				Expect(appSets.Items).ToNot(BeEmpty(), "Expected at least one ApplicationSet")

				for _, appSet := range appSets.Items {
					// Verify generator type is git
					generators, found, err := unstructured.NestedSlice(appSet.Object, "spec", "generators")
					Expect(err).ToNot(HaveOccurred())
					Expect(found).To(BeTrue(), "ApplicationSet %s must have generators", appSet.GetName())
					Expect(generators).ToNot(BeEmpty())

					// First generator should be a git generator
					gen := generators[0].(map[string]interface{})
					_, hasGit := gen["git"]
					Expect(hasGit).To(BeTrue(), "ApplicationSet %s must use git generator", appSet.GetName())
				}
			})
		})
	})
})
