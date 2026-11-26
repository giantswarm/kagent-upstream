package crconvert

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	backupLabel = "kagent.dev/cr-conversion-backup"
	// A Secret holds at most 1 MiB; a part stays well below.
	partBytes = 768 << 10
)

// Report counts what Backup or Apply did per kind.
type Report struct {
	Saved     map[string]int `json:"saved,omitempty"`
	Converted map[string]int `json:"converted,omitempty"`
	Created   map[string]int `json:"created,omitempty"`
	Gone      map[string]int `json:"gone,omitempty"`
}

// Backup saves every object of Kinds in every namespace, without its status
// and server-managed metadata, into Secrets named <name>-<n> in namespace. A
// backup that exists is kept as it is: it holds the objects as the 1.x CRDs
// served them, which a second run after the CRD upgrade could not read again.
func Backup(ctx context.Context, c client.Client, namespace, name string) (Report, error) {
	report := Report{Saved: map[string]int{}}
	existing, err := backupSecrets(ctx, c, namespace, name)
	if err != nil {
		return report, err
	}
	if len(existing) > 0 {
		return report, nil
	}
	var entries [][2]string
	for _, kind := range Kinds {
		list := &unstructured.UnstructuredList{}
		list.SetGroupVersionKind(schema.GroupVersionKind{Group: Group, Version: Version, Kind: kind + "List"})
		if err := c.List(ctx, list); err != nil {
			if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
				continue
			}
			return report, fmt.Errorf("list %s: %w", kind, err)
		}
		for i := range list.Items {
			obj := clean(&list.Items[i])
			data, err := json.Marshal(obj.Object)
			if err != nil {
				return report, fmt.Errorf("encode %s %s/%s: %w", kind, obj.GetNamespace(), obj.GetName(), err)
			}
			entries = append(entries, [2]string{kind + "." + obj.GetNamespace() + "." + obj.GetName() + ".json", string(data)})
			report.Saved[kind]++
		}
	}
	parts := []map[string][]byte{{}}
	size := 0
	for _, entry := range entries {
		if len(entry[1]) > partBytes {
			return report, fmt.Errorf("%s holds %d bytes, more than a backup part", entry[0], len(entry[1]))
		}
		if size+len(entry[1]) > partBytes {
			parts = append(parts, map[string][]byte{})
			size = 0
		}
		parts[len(parts)-1][entry[0]] = []byte(entry[1])
		size += len(entry[1])
	}
	for i, data := range parts {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: name + "-" + strconv.Itoa(i), Namespace: namespace,
				Labels:      map[string]string{backupLabel: name},
				Annotations: map[string]string{backupLabel + "-parts": strconv.Itoa(len(parts))},
			},
			Data: data,
		}
		if err := c.Create(ctx, secret); err != nil {
			return report, fmt.Errorf("save backup part %s: %w", secret.Name, err)
		}
	}
	return report, nil
}

// Load reads a backup Backup saved, in Kinds order.
func Load(ctx context.Context, c client.Client, namespace, name string) ([]*unstructured.Unstructured, error) {
	secrets, err := backupSecrets(ctx, c, namespace, name)
	if err != nil {
		return nil, err
	}
	if len(secrets) == 0 {
		return nil, fmt.Errorf("no backup %s in %s", name, namespace)
	}
	want, err := strconv.Atoi(secrets[0].Annotations[backupLabel+"-parts"])
	if err != nil || want != len(secrets) {
		return nil, fmt.Errorf("backup %s in %s has %d parts, its first names %q", name, namespace, len(secrets), secrets[0].Annotations[backupLabel+"-parts"])
	}
	var objects []*unstructured.Unstructured
	for _, secret := range secrets {
		for key, data := range secret.Data {
			obj := &unstructured.Unstructured{}
			if err := json.Unmarshal(data, &obj.Object); err != nil {
				return nil, fmt.Errorf("decode %s of %s: %w", key, secret.Name, err)
			}
			objects = append(objects, obj)
		}
	}
	slices.SortStableFunc(objects, func(a, b *unstructured.Unstructured) int {
		if d := slices.Index(Kinds, a.GetKind()) - slices.Index(Kinds, b.GetKind()); d != 0 {
			return d
		}
		if a.GetNamespace() != b.GetNamespace() {
			return strings.Compare(a.GetNamespace(), b.GetNamespace())
		}
		return strings.Compare(a.GetName(), b.GetName())
	})
	return objects, nil
}

// Apply writes the v1.0.1 shape of every backed-up object that the live
// cluster still holds, and the objects its conversion creates. It fails on an
// object the API server stored without a field it was sent, so a field the
// installed CRDs do not carry is never dropped silently.
func Apply(ctx context.Context, c client.Client, namespace, name string) (Report, error) {
	report := Report{Converted: map[string]int{}, Created: map[string]int{}, Gone: map[string]int{}}
	objects, err := Load(ctx, c, namespace, name)
	if err != nil {
		return report, err
	}
	for _, saved := range objects {
		converted, err := Convert(saved)
		if err != nil {
			return report, err
		}
		if !converted.Changed {
			continue
		}
		live := &unstructured.Unstructured{}
		live.SetGroupVersionKind(saved.GroupVersionKind())
		if err := c.Get(ctx, client.ObjectKeyFromObject(saved), live); err != nil {
			if apierrors.IsNotFound(err) {
				report.Gone[saved.GetKind()]++
				continue
			}
			return report, fmt.Errorf("read %s %s/%s: %w", saved.GetKind(), saved.GetNamespace(), saved.GetName(), err)
		}
		for _, created := range converted.Created {
			created.SetOwnerReferences([]metav1.OwnerReference{{
				APIVersion: live.GetAPIVersion(), Kind: live.GetKind(), Name: live.GetName(), UID: live.GetUID(),
			}})
			if err := createOrMatch(ctx, c, created); err != nil {
				return report, err
			}
			report.Created[created.GetKind()]++
		}
		live.Object["spec"] = converted.Object.Object["spec"]
		if err := c.Update(ctx, live); err != nil {
			return report, fmt.Errorf("write %s %s/%s: %w", saved.GetKind(), saved.GetNamespace(), saved.GetName(), err)
		}
		if !Contains(live.Object["spec"], converted.Object.Object["spec"]) {
			return report, fmt.Errorf("%s %s/%s was stored without fields it was sent: the installed CRD lacks them", saved.GetKind(), saved.GetNamespace(), saved.GetName())
		}
		report.Converted[saved.GetKind()]++
	}
	return report, nil
}

func createOrMatch(ctx context.Context, c client.Client, obj *unstructured.Unstructured) error {
	want := obj.Object["spec"]
	err := c.Create(ctx, obj)
	if apierrors.IsAlreadyExists(err) {
		existing := &unstructured.Unstructured{}
		existing.SetGroupVersionKind(obj.GroupVersionKind())
		if err := c.Get(ctx, client.ObjectKeyFromObject(obj), existing); err != nil {
			return fmt.Errorf("read %s %s/%s: %w", obj.GetKind(), obj.GetNamespace(), obj.GetName(), err)
		}
		if !Contains(existing.Object["spec"], want) {
			return fmt.Errorf("%s %s/%s exists with another spec", obj.GetKind(), obj.GetNamespace(), obj.GetName())
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("create %s %s/%s: %w", obj.GetKind(), obj.GetNamespace(), obj.GetName(), err)
	}
	if !Contains(obj.Object["spec"], want) {
		return fmt.Errorf("%s %s/%s was stored without fields it was sent: the installed CRD lacks them", obj.GetKind(), obj.GetNamespace(), obj.GetName())
	}
	return nil
}

func backupSecrets(ctx context.Context, c client.Client, namespace, name string) ([]corev1.Secret, error) {
	list := &corev1.SecretList{}
	if err := c.List(ctx, list, client.InNamespace(namespace), client.MatchingLabels{backupLabel: name}); err != nil {
		return nil, fmt.Errorf("list backup %s in %s: %w", name, namespace, err)
	}
	slices.SortFunc(list.Items, func(a, b corev1.Secret) int { return strings.Compare(a.Name, b.Name) })
	return list.Items, nil
}

func clean(obj *unstructured.Unstructured) *unstructured.Unstructured {
	out := obj.DeepCopy()
	delete(out.Object, "status")
	for _, field := range []string{"managedFields", "resourceVersion", "uid", "generation", "creationTimestamp"} {
		unstructured.RemoveNestedField(out.Object, "metadata", field)
	}
	return out
}
