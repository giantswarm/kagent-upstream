package app

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestQuiesceDeployment(t *testing.T) {
	labels := map[string]string{"app": "kagent-controller"}
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "kagent-controller", Namespace: "kagent"},
		Spec:       appsv1.DeploymentSpec{Selector: &metav1.LabelSelector{MatchLabels: labels}},
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "kagent-controller-0", Namespace: "kagent", Labels: labels},
		Status: corev1.PodStatus{Phase: corev1.PodRunning}}
	other := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "ui", Namespace: "kagent", Labels: map[string]string{"app": "ui"}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning}}
	clientset := fake.NewClientset(deployment, pod, other)
	replicas := int32(2)
	clientset.PrependReactor("get", "deployments", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() != "scale" {
			return false, nil, nil
		}
		return true, &autoscalingv1.Scale{ObjectMeta: metav1.ObjectMeta{Name: "kagent-controller", Namespace: "kagent"},
			Spec: autoscalingv1.ScaleSpec{Replicas: replicas}}, nil
	})
	clientset.PrependReactor("update", "deployments", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() != "scale" {
			return false, nil, nil
		}
		scale := action.(k8stesting.UpdateAction).GetObject().(*autoscalingv1.Scale)
		replicas = scale.Spec.Replicas
		require.NoError(t, clientset.Tracker().Delete(corev1.SchemeGroupVersion.WithResource("pods"), "kagent", pod.Name))
		return true, scale, nil
	})

	require.NoError(t, QuiesceDeployment(t.Context(), clientset, "kagent", "kagent-controller", time.Minute))
	require.Equal(t, int32(0), replicas)
	pods, err := clientset.CoreV1().Pods("kagent").List(t.Context(), metav1.ListOptions{})
	require.NoError(t, err)
	require.Len(t, pods.Items, 1, "only the controller's pods are waited for")

	require.NoError(t, QuiesceDeployment(t.Context(), clientset, "kagent", "absent", time.Minute), "a missing Deployment is quiet")
}
