package integration_tests

// ArgoCD Spoke RBAC Guardrail Tests
//
// These validate the least-privilege posture granted to the hub's ArgoCD
// capability role on a registered spoke cluster (see the argocd_spoke_capability
// access entry and the argocd-hub-read-all / argocd-hub-deploy ClusterRoles in
// modernisation-platform-environments cluster/argocd.tf).
//
// The capability role holds NO EKS access policy (no cluster-admin); all of its
// Kubernetes authorization comes from these ClusterRoles bound to the
// "argocd-hub" group declared on the access entry. These specs guard against
// that posture silently drifting: read must stay read-only, deploy must not be
// able to escalate cluster RBAC / CRDs / webhooks, and the bindings must target
// the argocd-hub group (the wiring the whole registration depends on).
//
// Runs only on a registered spoke, detected by the argocd-hub-deploy ClusterRole.

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("ArgoCD Spoke RBAC", Label("argocd", "spoke"), func() {

	const (
		readRole   = "argocd-hub-read-all"
		deployRole = "argocd-hub-deploy"
		hubGroup   = "argocd-hub"
	)

	var isSpoke bool

	BeforeEach(func() {
		_, err := clientset.RbacV1().ClusterRoles().Get(ctx, deployRole, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			isSpoke = false
			return
		}
		Expect(err).ToNot(HaveOccurred())
		isSpoke = true
	})

	Context("when the cluster is a registered ArgoCD spoke", func() {

		BeforeEach(func() {
			if !isSpoke {
				Skip("Not a registered ArgoCD spoke (argocd-hub-deploy ClusterRole not found)")
			}
		})

		// Spec 1 — the read role must never grant write.
		It("grants the read ClusterRole only get/list/watch", func() {
			cr, err := clientset.RbacV1().ClusterRoles().Get(ctx, readRole, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())

			allowed := map[string]bool{"get": true, "list": true, "watch": true}
			var badVerbs []string
			for _, rule := range cr.Rules {
				for _, v := range rule.Verbs {
					if !allowed[v] {
						badVerbs = append(badVerbs, v)
					}
				}
			}
			Expect(badVerbs).To(BeEmpty(),
				"read ClusterRole %s must only grant get/list/watch; found: %v", readRole, badVerbs)
		})

		// Spec 2 — the deploy role must not be able to escalate privilege.
		It("does not let the deploy ClusterRole write cluster RBAC, CRDs or webhooks", func() {
			cr, err := clientset.RbacV1().ClusterRoles().Get(ctx, deployRole, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())

			writeVerbs := map[string]bool{
				"create": true, "update": true, "patch": true,
				"delete": true, "deletecollection": true, "*": true,
			}
			// Cluster-scoped resources whose write would enable escalation, keyed by API group.
			sensitive := map[string]string{
				"clusterroles":                    "rbac.authorization.k8s.io",
				"clusterrolebindings":             "rbac.authorization.k8s.io",
				"customresourcedefinitions":       "apiextensions.k8s.io",
				"validatingwebhookconfigurations": "admissionregistration.k8s.io",
				"mutatingwebhookconfigurations":   "admissionregistration.k8s.io",
			}

			matchesGroup := func(rule rbacv1.PolicyRule, group string) bool {
				for _, g := range rule.APIGroups {
					if g == group || g == "*" {
						return true
					}
				}
				return false
			}
			matchesResource := func(rule rbacv1.PolicyRule, resource string) bool {
				for _, r := range rule.Resources {
					if r == resource || r == "*" {
						return true
					}
				}
				return false
			}
			hasWrite := func(rule rbacv1.PolicyRule) bool {
				for _, v := range rule.Verbs {
					if writeVerbs[v] {
						return true
					}
				}
				return false
			}

			var offending []string
			for res, group := range sensitive {
				for _, rule := range cr.Rules {
					if matchesGroup(rule, group) && matchesResource(rule, res) && hasWrite(rule) {
						offending = append(offending, res+"."+group)
						break
					}
				}
			}
			Expect(offending).To(BeEmpty(),
				"deploy ClusterRole %s must not grant write on cluster-scoped RBAC/CRDs/webhooks; offending: %v",
				deployRole, offending)
		})

		// Spec 3 — both ClusterRoles must be bound to the argocd-hub group that
		// the access entry declares; a mismatch here silently grants nothing.
		It("binds both ClusterRoles to the argocd-hub group", func() {
			for _, name := range []string{readRole, deployRole} {
				crb, err := clientset.RbacV1().ClusterRoleBindings().Get(ctx, name, metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred(), "expected ClusterRoleBinding %s", name)

				Expect(crb.RoleRef.Kind).To(Equal("ClusterRole"))
				Expect(crb.RoleRef.Name).To(Equal(name))

				boundToGroup := false
				for _, s := range crb.Subjects {
					if s.Kind == "Group" && s.Name == hubGroup {
						boundToGroup = true
					}
				}
				Expect(boundToGroup).To(BeTrue(),
					"ClusterRoleBinding %s must bind Group %q", name, hubGroup)
			}
		})
	})
})
