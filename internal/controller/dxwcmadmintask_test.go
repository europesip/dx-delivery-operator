// SPDX-License-Identifier: MIT
// Copyright (c) 2026 EUROPESIP

package controller

import (
	"testing"

	dxv1 "github.com/europesip/dx-delivery-operator/api/v1alpha1"
)

func TestAdminQuery(t *testing.T) {
	s := &dxv1.DXWCMAdminTaskSpec{Task: dxv1.WCMAdminMemberFixer, Libraries: []string{"orbita"}, PreserveDates: true,
		RestrictOn:  []dxv1.WCMItemType{"Content", "SiteArea"},
		MemberFixer: &dxv1.MemberFixerOptions{AltDn: "update", MismatchedId: "remove", NoRealmDn: true}}
	q := adminQuery(s, "orbita")
	want := map[string]string{"library": "orbita", "preserve_dates": "true", "restrictOn": "Content,SiteArea", "altDn": "update", "mismatchedId": "remove", "noRealmDn": "true"}
	for k, v := range want {
		if q.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, q.Get(k), v)
		}
	}
	if q.Has("fix") || q.Has("invalidDn") || q.Has("MOD") {
		t.Errorf("report mode must not send fix, unset options or MOD: %v", q)
	}
	s.Fix = true
	if adminQuery(s, "x").Get("fix") != "true" {
		t.Error("fix")
	}
	w := &dxv1.DXWCMAdminTaskSpec{Task: dxv1.WCMAdminWorkflowControlChecker, AllLibraries: true,
		WorkflowChecker: &dxv1.WorkflowCheckerOptions{RescheduleActions: true}, MemberFixer: nil}
	wq := adminQuery(w, "Web Content")
	if wq.Get("rescheduleActions") != "true" || wq.Has("updateDocSecurity") || wq.Get("library") != "Web Content" {
		t.Errorf("workflow query: %v", wq)
	}
}

func TestValidateAdmin(t *testing.T) {
	cases := []struct {
		s  dxv1.DXWCMAdminTaskSpec
		ok bool
	}{
		{dxv1.DXWCMAdminTaskSpec{Task: dxv1.WCMAdminMemberFixer, Libraries: []string{"a"}}, true},
		{dxv1.DXWCMAdminTaskSpec{Task: dxv1.WCMAdminMemberFixer}, false},
		{dxv1.DXWCMAdminTaskSpec{Task: dxv1.WCMAdminMemberFixer, Libraries: []string{"a"}, AllLibraries: true}, false},
		{dxv1.DXWCMAdminTaskSpec{Task: dxv1.WCMAdminWorkflowControlChecker, Libraries: []string{"a"}}, false},
		{dxv1.DXWCMAdminTaskSpec{Task: dxv1.WCMAdminWorkflowControlChecker, Libraries: []string{"a"}, WorkflowChecker: &dxv1.WorkflowCheckerOptions{UpdateDocSecurity: true}}, true},
		{dxv1.DXWCMAdminTaskSpec{Task: dxv1.WCMAdminWorkflowControlChecker, Libraries: []string{"a"}, WorkflowChecker: &dxv1.WorkflowCheckerOptions{PreserveSecurityInheritancePropagation: true, RescheduleActions: true}}, false},
		{dxv1.DXWCMAdminTaskSpec{Task: dxv1.WCMAdminMemberFixer, Libraries: []string{"a"}, WorkflowChecker: &dxv1.WorkflowCheckerOptions{UpdateDocSecurity: true}}, false},
	}
	for i, c := range cases {
		if err := validateAdmin(&c.s); (err == nil) != c.ok {
			t.Errorf("case %d: err=%v, want ok=%v", i, err, c.ok)
		}
	}
}
