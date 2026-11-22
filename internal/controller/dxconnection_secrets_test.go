package controller

import (
	"testing"

	dxv1 "github.com/europesip/hcl-dx-scriptapp-operator/api/v1alpha1"
)

func TestUsesSecret(t *testing.T) {
	s := &dxv1.DXConnectionSpec{CredentialsSecret: &dxv1.LocalObjectReference{Name: "p"}, TLSCASecret: &dxv1.LocalObjectReference{Name: "ca"},
		DXConnect: &dxv1.DXConnectSpec{CWCredentialsSecret: &dxv1.LocalObjectReference{Name: "cw"}, WASCredentialsSecret: &dxv1.LocalObjectReference{Name: "was"}}}
	for _, n := range []string{"p", "ca", "cw", "was"} {
		if !usesSecret(s, n) {
			t.Errorf("usesSecret(%q) = false", n)
		}
	}
	if usesSecret(s, "other") {
		t.Errorf("usesSecret(other) = true")
	}
}
