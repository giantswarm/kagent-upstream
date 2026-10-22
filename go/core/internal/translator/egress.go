package translator

import (
	"fmt"
	"net/url"
	"slices"
	"strings"

	"istio.io/istio/pkg/kube/krt"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/kagent-dev/kagent/go/api/v1alpha3"
	"github.com/kagent-dev/kagent/go/core/internal/egress"
)

// EgressGrantIssue describes why one referenced EgressPolicy contributes no grant.
type EgressGrantIssue struct {
	Policy  string
	Reason  string
	Message string
}

// EgressGrantResolution is the union of the individually valid policies a
// workload references. Admission checks the union against the workload's
// pinned runtime access (egress.FilterGrants).
type EgressGrantResolution struct {
	Access egress.Access
	Issues []EgressGrantIssue
}

// CompileEgressPolicies resolves the same-namespace grants of an Agent or a
// SandboxTemplate, each policy on its own: a missing or invalid policy
// contributes no grant and an issue, without removing the grants of the
// others. KRT tracks policy and Secret dependencies; only references enter the
// result, so Secret value rotation changes nothing.
func CompileEgressPolicies(ctx krt.HandlerContext, collections Collections, namespace string, refs []corev1.LocalObjectReference) EgressGrantResolution {
	var result EgressGrantResolution
	invalidRefs := ""
	if len(refs) > 16 {
		invalidRefs = "at most 16 egressPolicyRefs are supported"
	}
	for _, ref := range refs {
		if len(validation.IsDNS1123Subdomain(ref.Name)) != 0 {
			invalidRefs = fmt.Sprintf("invalid EgressPolicy reference %q", ref.Name)
			break
		}
	}
	for _, ref := range refs {
		key := namespace + "/" + ref.Name
		issue := EgressGrantIssue{Policy: key, Reason: v1alpha3.EgressPoliciesReasonInvalidEgressPolicy}
		if invalidRefs != "" {
			issue.Message = invalidRefs
			result.Issues = append(result.Issues, issue)
			continue
		}
		policy := krt.FetchOne(ctx, collections.EgressPolicies, krt.FilterKey(key))
		if policy == nil || !(*policy).DeletionTimestamp.IsZero() {
			issue.Reason, issue.Message = v1alpha3.EgressPoliciesReasonNotFound, "EgressPolicy not found"
			if policy != nil {
				issue.Message = "EgressPolicy is being deleted"
			}
			result.Issues = append(result.Issues, issue)
			continue
		}
		access, err := compileEgressPolicy(ctx, collections, namespace, *policy)
		if err != nil {
			issue.Message = err.Error()
			result.Issues = append(result.Issues, issue)
			continue
		}
		result.Access.Destinations = append(result.Access.Destinations, access.Destinations...)
		result.Access.Credentials = append(result.Access.Credentials, access.Credentials...)
	}
	slices.SortFunc(result.Issues, func(a, b EgressGrantIssue) int { return strings.Compare(a.Policy, b.Policy) })
	// A union that is valid on its own takes its canonical order, whatever the
	// order of the references; a conflicting one stays as requested.
	if canonical, err := egress.CanonicalAccess(result.Access.Destinations, result.Access.Credentials); err == nil {
		result.Access = canonical
	}
	return result
}

// compileEgressPolicy resolves one policy's destinations and credential
// references, its Secret keys included.
func compileEgressPolicy(ctx krt.HandlerContext, collections Collections, namespace string, policy *v1alpha3.EgressPolicy) (egress.Access, error) {
	if len(policy.Spec.Destinations) == 0 || len(policy.Spec.Destinations) > 64 {
		return egress.Access{}, NewValidationError("requires 1 to 64 destinations")
	}
	var destinations []string
	var credentials []egress.Credential
	for _, destination := range policy.Spec.Destinations {
		origin, err := egress.ParseOrigin(destination.Origin)
		if err != nil {
			return egress.Access{}, NewValidationError("%v", err)
		}
		destinations = append(destinations, origin)
		auth := destination.Authentication
		if auth == nil {
			continue
		}
		u, _ := url.Parse(origin)
		if u.Scheme != "https" || strings.Contains(u.Host, "*") {
			return egress.Access{}, NewValidationError("authentication requires an exact HTTPS origin")
		}
		if !egress.ValidCredentialHeader(auth.Header) {
			return egress.Access{}, NewValidationError("header %q cannot carry an egress credential", auth.Header)
		}
		secret := auth.SecretKeyRef
		// Substrate binds a credential to a hostname: the binding covers the
		// origin's host on every HTTPS port the policy allows.
		binding := egress.Credential{Hostname: u.Hostname(), Header: auth.Header}
		if auth.Prefix != nil {
			binding.Prefix = *auth.Prefix
		}
		if secret.Name == "" || secret.Key == "" || (secret.Optional != nil && *secret.Optional) {
			return egress.Access{}, NewValidationError("requires a non-optional Secret name and key")
		}
		binding.URI = egress.CredentialURI(egress.KubernetesSecretAuthority, namespace, secret.Name, secret.Key)
		if _, err := egress.CanonicalCredentials([]egress.Credential{binding}); err != nil {
			return egress.Access{}, NewValidationError("%v", err)
		}
		found := krt.FetchOne(ctx, collections.Secrets, krt.FilterKey(namespace+"/"+secret.Name))
		if found == nil {
			return egress.Access{}, fmt.Errorf("the Secret %q was not found", secret.Name)
		}
		if _, ok := (*found).Data[secret.Key]; !ok {
			return egress.Access{}, fmt.Errorf("the Secret %q is missing key %q", secret.Name, secret.Key)
		}
		credentials = append(credentials, binding)
	}
	access, err := egress.CanonicalAccess(destinations, credentials)
	if err != nil {
		return egress.Access{}, NewValidationError("%v", err)
	}
	return access, nil
}
