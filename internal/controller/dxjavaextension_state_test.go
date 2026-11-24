// SPDX-License-Identifier: MIT
// Copyright (c) 2026 EUROPESIP

package controller

import (
	"errors"
	"strings"
	"testing"

	admissionv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	dxv1 "github.com/europesip/dx-delivery-operator/api/v1alpha1"
	"github.com/europesip/dx-delivery-operator/internal/webhook"
)

const (
	stsName = "dx-deployment-web-engine"
	wantRef = "europesip-showcase@sha256:new"
	newHash = "sha256:new-template"
)

func testSTS(done bool) *appsv1.StatefulSet {
	s := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: stsName, Namespace: "jtm-poc", Generation: 2}}
	s.Spec.Replicas = ptr.To(int32(1))
	s.Spec.Template.Annotations = map[string]string{dxv1.AnnotationJavaExtensionsHash: newHash}
	s.Status.ObservedGeneration = 2
	if done { // a stale or finished status: the StatefulSet says everything is updated and Ready
		s.Status.UpdatedReplicas, s.Status.ReadyReplicas = 1, 1
		s.Status.CurrentRevision, s.Status.UpdateRevision = "rev-2", "rev-2"
	}
	return s
}

func testPod(hash, injected string, deleting, ready bool, conds ...corev1.PodCondition) corev1.Pod {
	p := corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: stsName + "-0", Namespace: "jtm-poc",
		OwnerReferences: []metav1.OwnerReference{{Kind: "StatefulSet", Name: stsName}},
		Annotations:     map[string]string{dxv1.AnnotationJavaExtensionsHash: hash, dxv1.AnnotationJavaExtensions: injected},
	}}
	if deleting {
		now := metav1.Now()
		p.DeletionTimestamp = &now
	}
	if ready {
		conds = append(conds, corev1.PodCondition{Type: corev1.PodReady, Status: corev1.ConditionTrue})
	}
	p.Status.Conditions = conds
	return p
}

// #61: the old pod is terminating and the new one does not exist yet, while the StatefulSet status still says
// "done": a normal rollout, never WebhookNotActive.
func TestJXStateReplacementWindow(t *testing.T) {
	old := testPod("sha256:old-template", "europesip-showcase@sha256:old", true, true)
	st := jxRolloutState(testSTS(true), []corev1.Pod{old}, wantRef)
	if st.done || st.reason != reasonJXRollingOut {
		t.Fatalf("replacement window: %+v, want RollingOut", st)
	}
	// The old pod still running (not deleted yet) with the old template: also RollingOut.
	old.DeletionTimestamp = nil
	st = jxRolloutState(testSTS(true), []corev1.Pod{old}, wantRef)
	if st.done || st.reason != reasonJXRollingOut {
		t.Fatalf("old pod still running: %+v, want RollingOut", st)
	}
}

// #61: the new pod is injected but cannot be scheduled (lack of memory): PodPending with the scheduler message.
func TestJXStatePodPending(t *testing.T) {
	p := testPod(newHash, wantRef, false, false, corev1.PodCondition{
		Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: corev1.PodReasonUnschedulable,
		Message: "0/6 nodes are available: 3 Insufficient memory.",
	})
	st := jxRolloutState(testSTS(false), []corev1.Pod{p}, wantRef)
	if st.done || st.reason != reasonJXPodPending || !strings.Contains(st.msg, "Insufficient memory") {
		t.Fatalf("pending pod: %+v, want PodPending with the scheduler message", st)
	}
}

// A pod created from the current template WITHOUT the injection: the webhook really did not act.
func TestJXStateNotInjected(t *testing.T) {
	p := testPod(newHash, "", false, true)
	st := jxRolloutState(testSTS(true), []corev1.Pod{p}, wantRef)
	if st.done || st.reason != reasonJXNotInjected {
		t.Fatalf("current pod without injection: %+v, want WebhookNotActive", st)
	}
	// And the finished case.
	p = testPod(newHash, "hello@sha256:x,"+wantRef, false, true)
	if st = jxRolloutState(testSTS(true), []corev1.Pod{p}, wantRef); !st.done {
		t.Fatalf("injected and Ready: %+v, want done", st)
	}
}

func javaExtWebhook(sel *metav1.LabelSelector) []admissionv1.MutatingWebhookConfiguration {
	path := webhook.JavaExtensionsPath
	return []admissionv1.MutatingWebhookConfiguration{{
		ObjectMeta: metav1.ObjectMeta{Name: "dxop-dx-delivery-operator-java-extensions"},
		Webhooks: []admissionv1.MutatingWebhook{{
			Name:              "java-extensions.dx.europesip.com",
			NamespaceSelector: sel,
			ClientConfig: admissionv1.WebhookClientConfig{Service: &admissionv1.ServiceReference{
				Namespace: "dxop-system", Name: "dxop-dx-delivery-operator-webhook", Path: &path,
			}},
		}},
	}}
}

func TestDiagnoseWebhook(t *testing.T) {
	sel := &metav1.LabelSelector{MatchLabels: map[string]string{dxv1.LabelJavaExtensionsNamespace: "enabled"}}
	enabled := map[string]string{dxv1.LabelJavaExtensionsNamespace: "enabled"}
	yes := func(string, string) (bool, error) { return true, nil }
	no := func(string, string) (bool, error) { return false, nil }

	if got := diagnoseWebhook(nil, enabled, yes); !strings.Contains(got, "no MutatingWebhookConfiguration") {
		t.Errorf("missing configuration: %q", got)
	}
	if got := diagnoseWebhook(javaExtWebhook(sel), map[string]string{}, yes); !strings.Contains(got, "namespaceSelector") {
		t.Errorf("label missing on the namespace: %q", got)
	}
	if got := diagnoseWebhook(javaExtWebhook(sel), enabled, no); !strings.Contains(got, "no ready endpoints") {
		t.Errorf("no endpoints: %q", got)
	}
	if got := diagnoseWebhook(javaExtWebhook(sel), enabled, func(string, string) (bool, error) { return false, errors.New("forbidden") }); !strings.Contains(got, "forbidden") {
		t.Errorf("endpoint read error: %q", got)
	}
	if got := diagnoseWebhook(javaExtWebhook(sel), enabled, yes); got != "" {
		t.Errorf("everything right: %q, want empty", got)
	}
}
