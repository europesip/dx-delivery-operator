// SPDX-License-Identifier: MIT
// Copyright (c) 2026 EUROPESIP

package controller

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	dxv1 "github.com/europesip/dx-delivery-operator/api/v1alpha1"
	"github.com/europesip/dx-delivery-operator/internal/wcmexport"
)

func libs(titles ...string) []wcmexport.Library {
	out := make([]wcmexport.Library, len(titles))
	for i, t := range titles {
		out[i] = wcmexport.Library{Title: t, Key: t}
	}
	return out
}

func TestResumable(t *testing.T) {
	res := &dxv1.WCMImportResume{Hash: "h", SourceRevision: "r", Chunks: [][]string{{"a"}, {"b"}}, Completed: 1}
	cases := []struct {
		name         string
		res          *dxv1.WCMImportResume
		hash, rev    string
		dryRun, want bool
	}{
		{"same spec and export", res, "h", "r", false, true},
		{"no plan", nil, "h", "r", false, false},
		{"spec changed", res, "h2", "r", false, false},
		{"export changed", res, "h", "r2", false, false},
		{"dry run", res, "h", "r", true, false},
		{"finished", &dxv1.WCMImportResume{Hash: "h", SourceRevision: "r", Chunks: [][]string{{"a"}}, Completed: 1}, "h", "r", false, false},
	}
	for _, c := range cases {
		if got := resumable(c.res, c.hash, c.rev, c.dryRun); got != c.want {
			t.Errorf("%s: resumable = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestNewResumeAndChunksOfPlan(t *testing.T) {
	chunks := [][]wcmexport.Library{libs("Orbita", "ThemeDS"), libs("Web Content")}
	res := newResume("h", "r", chunks, map[string]bool{"web content": true})
	if res.Attempt != 1 || len(res.Chunks) != 2 || res.Chunks[0][1] != "ThemeDS" {
		t.Fatalf("plan %+v", res)
	}
	if len(res.Existing) != 1 || res.Existing[0] != "web content" {
		t.Errorf("existing %v", res.Existing)
	}
	// the same libraries in another order: the saved chunks are rebuilt as planned
	got, ok := chunksOfPlan(res.Chunks, libs("Web Content", "themeds", "Orbita"))
	if !ok || len(got) != 2 || got[0][0].Title != "Orbita" || got[1][0].Title != "Web Content" {
		t.Errorf("chunksOfPlan = %v %v", got, ok)
	}
	if _, ok := chunksOfPlan(res.Chunks, libs("Orbita", "ThemeDS")); ok {
		t.Error("a planned library missing must invalidate the plan")
	}
	if _, ok := chunksOfPlan(res.Chunks, libs("Orbita", "ThemeDS", "Web Content", "New")); ok {
		t.Error("a library that is not in the plan must invalidate it")
	}
}

func TestResumeWait(t *testing.T) {
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	at := func(ago time.Duration) *metav1.Time { m := metav1.NewTime(now.Add(-ago)); return &m }
	cases := []struct {
		name string
		res  *dxv1.WCMImportResume
		want time.Duration
	}{
		{"cut while packing or uploading", &dxv1.WCMImportResume{}, 0},
		{"no chunk finished yet: 5 minutes", &dxv1.WCMImportResume{ChunkStartedAt: at(time.Minute)}, 4 * time.Minute},
		{"1.5 x the longest chunk", &dxv1.WCMImportResume{ChunkStartedAt: at(2 * time.Minute), LongestChunkSeconds: 600}, 13 * time.Minute},
		{"at least 3 minutes", &dxv1.WCMImportResume{ChunkStartedAt: at(0), LongestChunkSeconds: 10}, 3 * time.Minute},
		{"at most 30 minutes", &dxv1.WCMImportResume{ChunkStartedAt: at(0), LongestChunkSeconds: 7200}, 30 * time.Minute},
		{"already long enough ago", &dxv1.WCMImportResume{ChunkStartedAt: at(time.Hour), LongestChunkSeconds: 600}, 0},
	}
	for _, c := range cases {
		if got := resumeWait(c.res, now); got != c.want {
			t.Errorf("%s: wait %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCutWait(t *testing.T) {
	// no measured speed: 4 files/s with 50% margin: 7328 files → 45.8 min, minus 5 min already spent
	if got := cutWait(7328, 0, 0, 5*time.Minute); got < 40*time.Minute || got > 41*time.Minute {
		t.Errorf("default rate: %s", got)
	}
	// measured 6 files/s: 3000 files → 12.5 min - 2 min
	if got := cutWait(3000, 600, 100, 2*time.Minute); got < 10*time.Minute || got > 11*time.Minute {
		t.Errorf("measured rate: %s", got)
	}
	if got := cutWait(10, 0, 0, 0); got != cutWaitMin {
		t.Errorf("small chunk: %s, want the minimum", got)
	}
	if got := cutWait(1_000_000, 0, 0, 0); got != cutWaitMax {
		t.Errorf("huge chunk: %s, want the maximum", got)
	}
}
