package crconvert

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func object(kind, namespace, name string, spec map[string]any) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{Object: map[string]any{"spec": spec}}
	obj.SetAPIVersion(Group + "/" + Version)
	obj.SetKind(kind)
	obj.SetNamespace(namespace)
	obj.SetName(name)
	return obj
}

func TestConvertAgent(t *testing.T) {
	inline := map[string]any{"systemMessage": "hi", "modelConfig": "default"}
	harness := map[string]any{"workload": map[string]any{"image": "img", "toolImages": []any{map[string]any{"name": "go", "oci": "oci://go"}}}}
	converted, err := Convert(object("Agent", "team-a", "a", map[string]any{
		"template":         inline,
		"harness":          harness,
		"egress":           []any{"https://proxy.golang.org", "tls://db.example.com:5432"},
		"egressPolicyRefs": []any{map[string]any{"name": "shared"}},
	}))
	require.NoError(t, err)
	require.True(t, converted.Changed)
	require.Equal(t, map[string]any{
		"template":         map[string]any{"inline": inline},
		"harnessRef":       map[string]any{"name": "a-harness"},
		"egressPolicyRefs": []any{map[string]any{"name": "shared"}, map[string]any{"name": "a-egress"}},
	}, converted.Object.Object["spec"])
	require.Len(t, converted.Created, 2)
	require.Equal(t, "Harness", converted.Created[0].GetKind())
	require.Equal(t, harness, converted.Created[0].Object["spec"])
	require.Equal(t, "EgressPolicy", converted.Created[1].GetKind())
	require.Equal(t, map[string]any{"destinations": []any{
		map[string]any{"origin": "https://proxy.golang.org"}, map[string]any{"origin": "tls://db.example.com:5432"},
	}}, converted.Created[1].Object["spec"])

	byRef, err := Convert(object("Agent", "team-a", "b", map[string]any{
		"templateRef": map[string]any{"name": "t"}, "harnessRef": map[string]any{"name": "h"},
	}))
	require.NoError(t, err)
	require.Equal(t, map[string]any{"template": map[string]any{"ref": map[string]any{"name": "t"}}, "harnessRef": map[string]any{"name": "h"}},
		byRef.Object.Object["spec"])
	require.Empty(t, byRef.Created)

	again, err := Convert(converted.Object)
	require.NoError(t, err)
	require.False(t, again.Changed, "an Agent in the v1.0.1 shape is left alone")

	_, err = Convert(object("Agent", "team-a", "c", map[string]any{"template": inline, "templateRef": map[string]any{"name": "t"}}))
	require.Error(t, err)
}

func TestConvertModelConfig(t *testing.T) {
	converted, err := Convert(object("ModelConfig", "team-a", "m", map[string]any{"model": "x", "apiKeyPassthrough": true}))
	require.NoError(t, err)
	require.Equal(t, map[string]any{"model": "x", "tokenPassthrough": true}, converted.Object.Object["spec"])
	off, err := Convert(object("ModelConfig", "team-a", "n", map[string]any{"model": "x", "apiKeyPassthrough": false}))
	require.NoError(t, err)
	require.Equal(t, map[string]any{"model": "x"}, off.Object.Object["spec"])
}

func testClient(t *testing.T, objects ...client.Object) client.WithWatch {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	for _, kind := range Kinds {
		gv := schema.GroupVersion{Group: Group, Version: Version}
		scheme.AddKnownTypeWithName(gv.WithKind(kind), &unstructured.Unstructured{})
		scheme.AddKnownTypeWithName(gv.WithKind(kind+"List"), &unstructured.UnstructuredList{})
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
}

func TestBackupThenApply(t *testing.T) {
	ctx := t.Context()
	agent := object("Agent", "team-a", "a", map[string]any{
		"template": map[string]any{"systemMessage": "hi"},
		"harness":  map[string]any{"workload": map[string]any{"image": "img"}},
		"egress":   []any{"https://proxy.golang.org"},
	})
	model := object("ModelConfig", "team-a", "m", map[string]any{"model": "x", "apiKeyPassthrough": true})
	untouched := object("RemoteMCPServer", "team-a", "r", map[string]any{"url": "https://mcp.example"})
	c := testClient(t, agent, model, untouched)

	saved, err := Backup(ctx, c, "kagent", "crs")
	require.NoError(t, err)
	require.Equal(t, map[string]int{"Agent": 1, "ModelConfig": 1, "RemoteMCPServer": 1}, saved.Saved)
	again, err := Backup(ctx, c, "kagent", "crs")
	require.NoError(t, err)
	require.Empty(t, again.Saved, "a backup that exists is kept")

	applied, err := Apply(ctx, c, "kagent", "crs")
	require.NoError(t, err)
	require.Equal(t, map[string]int{"Agent": 1, "ModelConfig": 1}, applied.Converted)
	require.Equal(t, map[string]int{"Harness": 1, "EgressPolicy": 1}, applied.Created)

	live := object("Agent", "team-a", "a", nil)
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(live), live))
	require.Equal(t, map[string]any{"name": "a-harness"}, live.Object["spec"].(map[string]any)["harnessRef"])
	harness := object("Harness", "team-a", "a-harness", nil)
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(harness), harness))
	require.Equal(t, "a", harness.GetOwnerReferences()[0].Name)

	second, err := Apply(ctx, c, "kagent", "crs")
	require.NoError(t, err, "a second apply finds the objects it created and writes the same specs")
	require.Equal(t, applied.Converted, second.Converted)
}

func TestContainsSeesPrunedFields(t *testing.T) {
	sent := map[string]any{"workload": map[string]any{"image": "img", "toolImages": []any{"x"}}, "replicas": int64(2)}
	require.True(t, Contains(map[string]any{"workload": map[string]any{"image": "img", "toolImages": []any{"x"}}, "replicas": float64(2), "extra": 1}, sent))
	require.False(t, Contains(map[string]any{"workload": map[string]any{"image": "img"}, "replicas": int64(2)}, sent))
}
