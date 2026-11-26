// Package crconvert moves the line's 1.x api.kagent.dev/v1alpha3 objects to
// the kagent v1.0.1 shape of the same version. The CRDs keep v1alpha3, so no
// conversion webhook can do it, and once the v1.0.1 CRDs are installed the API
// server prunes the 1.x fields on every read: Backup saves every object as the
// 1.x CRDs serve it before the CRD upgrade, and Apply writes the converted
// objects from that backup after it.
package crconvert

import (
	"fmt"
	"maps"
	"slices"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Group is the API group and version both shapes share.
const (
	Group   = "api.kagent.dev"
	Version = "v1alpha3"
)

// Kinds are the kinds Backup saves, in the order Apply writes them: the
// objects an Agent refers to before the Agent.
var Kinds = []string{
	"ModelProviderConfig", "ModelConfig", "RemoteMCPServer", "EgressPolicy",
	"Harness", "AgentTemplate", "SandboxTemplate", "Agent",
}

// Converted is what one 1.x object becomes: the object itself and the objects
// it now refers to instead of holding inline.
type Converted struct {
	Object *unstructured.Unstructured
	// Created are new objects in the object's namespace: the Harness an
	// inline harness becomes and the EgressPolicy spec.egress becomes. Each is
	// owned by the object, so it leaves with it as the inline field did.
	Created []*unstructured.Unstructured
	Changed bool
}

// HarnessName and EgressPolicyName name the objects an Agent's inline harness
// and egress list become.
func HarnessName(agent string) string      { return agent + "-harness" }
func EgressPolicyName(agent string) string { return agent + "-egress" }

// Convert returns the v1.0.1 shape of obj, a 1.x object as Backup saved it.
// An object already in the v1.0.1 shape comes back unchanged.
func Convert(obj *unstructured.Unstructured) (Converted, error) {
	out := Converted{Object: obj.DeepCopy()}
	switch obj.GetKind() {
	case "Agent":
		return convertAgent(out)
	case "ModelConfig":
		return convertModelConfig(out)
	default:
		return out, nil
	}
}

func convertAgent(out Converted) (Converted, error) {
	agent := out.Object
	spec, _, err := unstructured.NestedMap(agent.Object, "spec")
	if err != nil {
		return out, fmt.Errorf("agent %s/%s: %w", agent.GetNamespace(), agent.GetName(), err)
	}
	if spec == nil {
		return out, nil
	}
	template := map[string]any{}
	if existing, ok := spec["template"].(map[string]any); ok {
		if _, inline := existing["inline"]; inline {
			template = existing
		} else if _, ref := existing["ref"]; ref && len(existing) == 1 {
			template = existing
		} else {
			template["inline"] = existing
			out.Changed = true
		}
	}
	if ref, ok := spec["templateRef"]; ok {
		if len(template) > 0 {
			return out, fmt.Errorf("agent %s/%s sets both template and templateRef", agent.GetNamespace(), agent.GetName())
		}
		template["ref"] = ref
		delete(spec, "templateRef")
		out.Changed = true
	}
	if len(template) > 0 {
		spec["template"] = template
	}

	if harness, ok := spec["harness"].(map[string]any); ok {
		if _, ref := spec["harnessRef"]; ref {
			return out, fmt.Errorf("agent %s/%s sets both harness and harnessRef", agent.GetNamespace(), agent.GetName())
		}
		created := owned(agent, "Harness", HarnessName(agent.GetName()))
		created.Object["spec"] = harness
		out.Created = append(out.Created, created)
		spec["harnessRef"] = map[string]any{"name": created.GetName()}
		delete(spec, "harness")
		out.Changed = true
	}

	if egress, ok := spec["egress"].([]any); ok {
		delete(spec, "egress")
		out.Changed = true
		if len(egress) > 0 {
			destinations := make([]any, 0, len(egress))
			for _, origin := range egress {
				destinations = append(destinations, map[string]any{"origin": origin})
			}
			created := owned(agent, "EgressPolicy", EgressPolicyName(agent.GetName()))
			created.Object["spec"] = map[string]any{"destinations": destinations}
			out.Created = append(out.Created, created)
			refs, _ := spec["egressPolicyRefs"].([]any)
			if !slices.ContainsFunc(refs, func(ref any) bool {
				m, ok := ref.(map[string]any)
				return ok && m["name"] == created.GetName()
			}) {
				refs = append(refs, map[string]any{"name": created.GetName()})
			}
			spec["egressPolicyRefs"] = refs
		}
	}
	agent.Object["spec"] = spec
	return out, nil
}

// The 1.x ModelConfig spec.apiKeyPassthrough is v1.0.1's tokenPassthrough.
func convertModelConfig(out Converted) (Converted, error) {
	spec, _, err := unstructured.NestedMap(out.Object.Object, "spec")
	if err != nil || spec == nil {
		return out, err
	}
	passthrough, ok := spec["apiKeyPassthrough"]
	if !ok {
		return out, nil
	}
	delete(spec, "apiKeyPassthrough")
	if enabled, _ := passthrough.(bool); enabled {
		if _, set := spec["tokenPassthrough"]; !set {
			spec["tokenPassthrough"] = true
		}
	}
	out.Object.Object["spec"] = spec
	out.Changed = true
	return out, nil
}

func owned(owner *unstructured.Unstructured, kind, name string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{Object: map[string]any{}}
	obj.SetAPIVersion(Group + "/" + Version)
	obj.SetKind(kind)
	obj.SetNamespace(owner.GetNamespace())
	obj.SetName(name)
	labels := maps.Clone(owner.GetLabels())
	if labels == nil {
		labels = map[string]string{}
	}
	labels["kagent.dev/converted-from"] = owner.GetName()
	obj.SetLabels(labels)
	return obj
}

// Contains reports whether every field of want is in got with the same value:
// a field the API server pruned on write, because the installed CRD does not
// know it, is missing from got.
func Contains(got, want any) bool {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return false
		}
		for key, value := range w {
			if !Contains(g[key], value) {
				return false
			}
		}
		return true
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return false
		}
		for i := range w {
			if !Contains(g[i], w[i]) {
				return false
			}
		}
		return true
	case int64, float64, int:
		return numeric(got) == numeric(want)
	default:
		return got == want
	}
}

func numeric(v any) any {
	switch n := v.(type) {
	case int:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return v
	}
}
