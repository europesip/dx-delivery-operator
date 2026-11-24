// SPDX-License-Identifier: MIT

/*
Copyright 2026 EUROPESIP.

Licensed under the MIT License (see LICENSE).
*/

package webhook

import (
	"context"
	"encoding/json"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	dxv1 "github.com/europesip/dx-delivery-operator/api/v1alpha1"
	"github.com/europesip/dx-delivery-operator/internal/javaext"
)

func TestInjector(t *testing.T) {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = dxv1.AddToScheme(s)
	ext := func(name, desired string) *dxv1.DXJavaExtension {
		return &dxv1.DXJavaExtension{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "portal"},
			Spec:       dxv1.DXJavaExtensionSpec{ConnectionRef: dxv1.LocalObjectReference{Name: "c"}, Artifact: dxv1.JavaArtifact{Ref: "h/r/" + name + ":1", PullSecrets: []dxv1.LocalObjectReference{{Name: "harbor"}}}},
			Status:     dxv1.DXJavaExtensionStatus{DesiredRef: desired},
		}
	}
	other := ext("other", "h/r/other@sha256:cc")
	other.Spec.Target.StatefulSet = "another-web-engine"
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(
		ext("showcase", "h/r/showcase@sha256:aa"),
		ext("notyet", ""), // not resolved yet: not injected
		other,
	).Build()
	h := &JavaExtensionInjector{Client: c, Decoder: admission.NewDecoder(s), Options: javaext.InjectOptions{InitImage: "init:1"}}

	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "dx-deployment-web-engine-0", Namespace: "portal",
			OwnerReferences: []metav1.OwnerReference{{Kind: "StatefulSet", Name: "dx-deployment-web-engine", APIVersion: "apps/v1", UID: "u"}}},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "web-engine", Image: "hcl/webengine:CF239"}}},
	}
	raw, _ := json.Marshal(pod)
	req := admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Create, Namespace: "portal", Object: runtime.RawExtension{Raw: raw}}}
	res := h.Handle(context.Background(), req)
	if !res.Allowed || len(res.Patches) == 0 {
		t.Fatalf("expected a patch: %+v", res.Result)
	}
	entries, container, err := EntriesFor(context.Background(), c, "portal", "dx-deployment-web-engine")
	if err != nil || container != "web-engine" || len(entries) != 1 || entries[0].Ref != "h/r/showcase@sha256:aa" || entries[0].File != "showcase.war" || entries[0].ContextRoot != "/showcase" {
		t.Fatalf("entries %+v %v", entries, err)
	}

	// A pod without extensions (another StatefulSet without any, or no owner) is allowed unchanged.
	pod.OwnerReferences[0].Name = "dx-deployment-haproxy"
	raw, _ = json.Marshal(pod)
	req.Object.Raw = raw
	if res := h.Handle(context.Background(), req); !res.Allowed || len(res.Patches) != 0 {
		t.Fatalf("expected no patch: %+v", res)
	}
}
