package controller

import (
	"strings"
	"testing"

	dxv1 "github.com/europesip/dx-delivery-operator/api/v1alpha1"
	"github.com/europesip/dx-delivery-operator/internal/wcmexport"
)

func TestExportLibrariesPlan(t *testing.T) {
	libs := []wcmexport.Library{
		{Title: "A", Description: "first", Bytes: 10, Files: 2},
		{Title: "Portal Site"},
		{Title: "B", Description: strings.Repeat("é", 400)},
		{Title: "C"},
	}
	plans := map[string]dxv1.WCMExportLibrary{
		"Portal Site": {Plan: dxv1.WCMPlanSkip, Reason: "never"},
		"C":           {Plan: dxv1.WCMPlanNotSelected, Reason: "not in include"},
	}
	out := exportLibraries(libs, plans, map[string]bool{"b": true})
	if len(out) != 4 {
		t.Fatalf("len %d", len(out))
	}
	if out[0].Plan != dxv1.WCMPlanImport || out[0].Description != "first" || out[0].Bytes != 10 || out[0].Files != 2 {
		t.Fatalf("A: %#v", out[0])
	}
	if out[1].Plan != dxv1.WCMPlanSkip || out[1].Reason != "never" {
		t.Fatalf("Portal Site: %#v", out[1])
	}
	if !out[2].ExistsInTarget || len([]rune(out[2].Description)) != 300 {
		t.Fatalf("B: exists %v, %d runes", out[2].ExistsInTarget, len([]rune(out[2].Description)))
	}
	if out[3].Plan != dxv1.WCMPlanNotSelected {
		t.Fatalf("C: %#v", out[3])
	}
}
