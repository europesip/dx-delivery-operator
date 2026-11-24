// SPDX-License-Identifier: MIT
// Copyright (c) 2026 EUROPESIP

package controller

import (
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	dxv1 "github.com/europesip/dx-delivery-operator/api/v1alpha1"
)

// #57: an unfinished rollout keeps waiting (and the reconcile asks to be requeued) until the pods are Ready; only a
// failure in the pods or the timeout rolls it back.
func TestRolloutFailure(t *testing.T) {
	now := time.Date(2026, 11, 23, 22, 0, 0, 0, time.UTC)
	started := metav1.NewTime(now.Add(-5 * time.Minute))
	if got := rolloutFailure("", &started, 30*time.Minute, now); got != "" {
		t.Errorf("5 min into a 30 min rollout: %q, want keep waiting", got)
	}
	if got := rolloutFailure("", nil, 30*time.Minute, now); got != "" {
		t.Errorf("no start time: %q, want keep waiting", got)
	}
	if got := rolloutFailure("pod x: the init container failed", &started, 30*time.Minute, now); !strings.Contains(got, "init container") {
		t.Errorf("pod failure not reported: %q", got)
	}
	if got := rolloutFailure("", &started, 2*time.Minute, now); !strings.Contains(got, "not finished after 2m0s") {
		t.Errorf("timeout: %q", got)
	}
	if jxPoll <= 0 || jxPoll > 15*time.Second {
		t.Errorf("jxPoll = %s: a RollingOut extension must be requeued within ~15 s", jxPoll)
	}
}

func TestRolloutTimeoutDefaults(t *testing.T) {
	e := &dxv1.DXJavaExtension{}
	r := &DXJavaExtensionReconciler{}
	if got := r.rolloutTimeout(e); got != jxDefaultTimeout {
		t.Errorf("default = %s, want %s", got, jxDefaultTimeout)
	}
	r.RolloutTimeout = 10 * time.Minute
	if got := r.rolloutTimeout(e); got != 10*time.Minute {
		t.Errorf("operator default = %s, want 10m", got)
	}
	e.Spec.RolloutTimeout = &metav1.Duration{Duration: 3 * time.Minute}
	if got := r.rolloutTimeout(e); got != 3*time.Minute {
		t.Errorf("spec = %s, want 3m", got)
	}
}

// A Web Engine pod that changes (Ready, failed) reconciles the extensions of its StatefulSet at once.
func TestRequestsFor(t *testing.T) {
	mk := func(name, sts string) dxv1.DXJavaExtension {
		e := dxv1.DXJavaExtension{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "jtm-poc"}}
		e.Spec.Target.StatefulSet = sts
		return e
	}
	items := []dxv1.DXJavaExtension{mk("a", ""), mk("b", ""), mk("c", "other-web-engine")}
	def := items[0].StatefulSetName()
	got := requestsFor(items, def)
	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "b" || got[0].Namespace != "jtm-poc" {
		t.Errorf("requests for %s = %v, want a and b", def, got)
	}
	if got := requestsFor(items, "other-web-engine"); len(got) != 1 || got[0].Name != "c" {
		t.Errorf("requests for other-web-engine = %v, want c", got)
	}
	if got := requestsFor(items, "nope"); len(got) != 0 {
		t.Errorf("requests for an unknown StatefulSet = %v", got)
	}
}
