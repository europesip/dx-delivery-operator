package controller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	dxv1 "github.com/europesip/hcl-dx-scriptapp-operator/api/v1alpha1"
	"github.com/europesip/hcl-dx-scriptapp-operator/internal/dx"
	"github.com/europesip/hcl-dx-scriptapp-operator/internal/source"
	"github.com/europesip/hcl-dx-scriptapp-operator/internal/wcmexport"
)

// The WCM part of DXWCMLibraryExport and DXExport: the libraries are checked, exported one by one with the WCM data
// module into one dxFileTransfer folder (DX merges them into one JCR tree), downloaded as one zip and checked.

// resolveLibraries checks that every title is a library of the portal and returns the titles as DX writes them.
func resolveLibraries(ctx context.Context, c *dx.Client, vp, where string, titles []string) ([]string, error) {
	libs, err := c.ListLibraries(ctx, vp)
	if err != nil {
		return nil, fmt.Errorf("libraries of %s: %w", where, err)
	}
	have := map[string]string{}
	for _, l := range libs {
		have[strings.ToLower(l.Title)] = l.Title
	}
	out := make([]string, len(titles))
	var missing []string
	for i, t := range titles {
		if real, ok := have[strings.ToLower(t)]; ok {
			out[i] = real
		} else {
			missing = append(missing, t)
		}
	}
	if len(missing) > 0 {
		return nil, fatal(reasonLibNotFound, "not in %s: %s; nothing was exported", where, strings.Join(missing, ", "))
	}
	return out, nil
}

// wcmExportJob is one export of libraries into a zip.
type wcmExportJob struct {
	Client  *dx.Client
	VP      string
	Where   string
	Titles  []string // as resolveLibraries returns them
	Sub     string   // dxFileTransfer folder
	ZipPath string
	Timeout time.Duration
	// OnLibrary is called before each library (1-based); OnDownload before the download; OnCleanupError when the
	// dxFileTransfer folder could not be deleted.
	OnLibrary      func(i, n int, title string)
	OnDownload     func()
	OnCleanupError func(error)
}

// run exports, downloads and checks: the size of the zip and the libraries found in it (sorted by title).
func (j wcmExportJob) run(ctx context.Context) (int64, []dxv1.WCMExportedLibrary, error) {
	c := j.Client
	_ = c.FileTransferDeleteDir(ctx, j.Sub) // leftovers of a run that was cut
	if err := c.FileTransferCreateDir(ctx, j.Sub); err != nil {
		return 0, nil, fmt.Errorf("dxFileTransfer: %w", err)
	}
	defer func() {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
		defer cancel()
		if err := c.FileTransferDeleteDir(cctx, j.Sub); err != nil && j.OnCleanupError != nil {
			j.OnCleanupError(err)
		}
	}()
	for i, t := range j.Titles {
		if ctx.Err() != nil {
			return 0, nil, ctx.Err()
		}
		if j.OnLibrary != nil {
			j.OnLibrary(i+1, len(j.Titles), t)
		}
		if _, err := c.DataModuleExport(ctx, j.VP, t, dx.FileTransferRoot+"/"+j.Sub, j.Timeout); err != nil {
			if errors.Is(err, dx.ErrCutByProxy) || strings.Contains(err.Error(), "DX export failed") {
				return 0, nil, fatal(reasonExportFailed, "library %s: %v", t, err)
			}
			return 0, nil, fmt.Errorf("library %s: %w", t, err)
		}
	}
	if j.OnDownload != nil {
		j.OnDownload()
	}
	n, err := c.FileTransferDownload(ctx, j.Sub, j.ZipPath)
	if err != nil {
		return 0, nil, err
	}
	unpacked := j.ZipPath + ".check"
	defer os.RemoveAll(unpacked) //nolint:errcheck
	if _, err := source.ExtractArchiveProgressContext(ctx, j.ZipPath, unpacked, nil); err != nil {
		return 0, nil, fatal(reasonExportFailed, "the zip downloaded from DX cannot be read: %v", err)
	}
	exp, err := wcmexport.Open(unpacked)
	if err != nil {
		return 0, nil, fatal(reasonExportFailed, "the export downloaded from DX: %v", err)
	}
	got := map[string]wcmexport.Library{}
	for _, l := range exp.Libraries {
		got[strings.ToLower(l.Title)] = l
	}
	var libs []dxv1.WCMExportedLibrary
	for _, t := range j.Titles {
		l, ok := got[strings.ToLower(t)]
		if !ok {
			return 0, nil, fatal(reasonExportFailed, "DX said %s was exported, but it is not in the downloaded export", t)
		}
		libs = append(libs, dxv1.WCMExportedLibrary{Title: l.Title, Bytes: l.Bytes, Files: l.Files})
	}
	sort.Slice(libs, func(i, k int) bool { return libs[i].Title < libs[k].Title })
	return n, libs, nil
}

// exportWorkDir is an empty work folder of a run (removed by the caller).
func exportWorkDir(root, kind, ns, name string) (string, error) {
	work := filepath.Join(root, kind, ns+"-"+name)
	_ = os.RemoveAll(work)
	return work, os.MkdirAll(work, 0o700)
}
