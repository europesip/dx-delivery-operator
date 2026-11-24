// SPDX-License-Identifier: MIT
// Copyright (c) 2026 EUROPESIP

package controller

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	dxv1 "github.com/europesip/dx-delivery-operator/api/v1alpha1"
)

// Issue #72: with the startup hook a current pod without the annotation is still starting (the script annotates
// it when it downloads the extensions); Ready without it means the script did not run; a crash loop fails it.
func TestJXStateStartupHook(t *testing.T) {
	p := testPod(newHash, "", false, false)
	st := jxRolloutStateMode(testSTS(false), []corev1.Pod{p}, wantRef, dxv1.DeliveryStartupHook, "web-engine")
	if st.done || st.failure != "" || st.reason != reasonJXRollingOut || !strings.Contains(st.msg, "start-up") {
		t.Fatalf("starting pod: %+v", st)
	}
	p = testPod(newHash, "", false, true)
	st = jxRolloutStateMode(testSTS(false), []corev1.Pod{p}, wantRef, dxv1.DeliveryStartupHook, "web-engine")
	if st.reason != reasonJXHookNotRun {
		t.Fatalf("ready without the extension: %+v", st)
	}
	p = testPod(newHash, "", false, false)
	p.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "web-engine", RestartCount: 2,
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}}}
	st = jxRolloutStateMode(testSTS(false), []corev1.Pod{p}, wantRef, dxv1.DeliveryStartupHook, "web-engine")
	if st.failure == "" {
		t.Fatalf("crash loop must fail the rollout: %+v", st)
	}
	p = testPod(newHash, wantRef, false, true)
	if st = jxRolloutStateMode(testSTS(true), []corev1.Pod{p}, wantRef, dxv1.DeliveryStartupHook, "web-engine"); !st.done {
		t.Fatalf("loaded and ready: %+v", st)
	}
	// the webhook mode is unchanged: a current pod without the injection is WebhookNotActive
	p = testPod(newHash, "", false, false)
	if st = jxRolloutState(testSTS(false), []corev1.Pod{p}, wantRef); st.reason != reasonJXNotInjected {
		t.Fatalf("webhook mode: %+v", st)
	}
}

func TestSeedJobName(t *testing.T) {
	n := seedJobName(strings.Repeat("a", 80), "sha256:0123456789abcdef")
	if len(n) > 63 || !strings.HasSuffix(n, "-01234567") || !strings.HasPrefix(n, "dx-jx-seed-") {
		t.Fatalf("name %q (%d)", n, len(n))
	}
}
