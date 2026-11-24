// SPDX-License-Identifier: MIT
// Copyright (c) 2026 EUROPESIP

package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	dxv1 "github.com/europesip/dx-delivery-operator/api/v1alpha1"
	"github.com/europesip/dx-delivery-operator/internal/dx"
	"github.com/europesip/dx-delivery-operator/internal/source"
	"github.com/europesip/dx-delivery-operator/internal/wcmexport"
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
	n, err := j.fetch(ctx)
	if err != nil {
		return 0, nil, err
	}
	return j.check(ctx, n)
}

// fetch exports the libraries and downloads them into ZipPath: through DXConnect on DX Core (one request for every
// library), with the WCM data module and dxFileTransfer on DX Compose.
func (j wcmExportJob) fetch(ctx context.Context) (int64, error) {
	c := j.Client
	if c.UsesDXConnect() {
		if j.OnLibrary != nil {
			j.OnLibrary(1, 1, strings.Join(j.Titles, ", ")+" (DXConnect, DX Core)")
		}
		tarPath := j.ZipPath + ".dxconnect.tar"
		defer os.Remove(tarPath) //nolint:errcheck
		if _, err := c.DXConnectExportLibraries(ctx, j.VP, j.Titles, tarPath, j.Timeout); err != nil {
			if strings.Contains(err.Error(), "HTTP 4") || strings.Contains(err.Error(), "HTTP 5") || strings.Contains(err.Error(), "without a path") || strings.Contains(err.Error(), "DXConnect did not answer") {
				return 0, fatal(reasonExportFailed, "%v", err)
			}
			return 0, err
		}
		if j.OnDownload != nil {
			j.OnDownload()
		}
		return dxConnectToZip(ctx, tarPath, j.ZipPath)
	}
	_ = c.FileTransferDeleteDir(ctx, j.Sub) // leftovers of a run that was cut
	if err := c.FileTransferCreateDir(ctx, j.Sub); err != nil {
		return 0, fmt.Errorf("dxFileTransfer: %w", err)
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
			return 0, ctx.Err()
		}
		if j.OnLibrary != nil {
			j.OnLibrary(i+1, len(j.Titles), t)
		}
		if _, err := c.DataModuleExport(ctx, j.VP, t, dx.FileTransferRoot+"/"+j.Sub, j.Timeout); err != nil {
			if errors.Is(err, dx.ErrCutByProxy) || strings.Contains(err.Error(), "DX export failed") {
				return 0, fatal(reasonExportFailed, "library %s: %v", t, err)
			}
			return 0, fmt.Errorf("library %s: %w", t, err)
		}
	}
	if j.OnDownload != nil {
		j.OnDownload()
	}
	return c.FileTransferDownload(ctx, j.Sub, j.ZipPath)
}

// dxConnectToZip turns the archive of DXConnect (a tar, WCM_Library-<time>/<library>/<JCR export>, one folder per
// library) into the zip of the operator: one JCR export (554ee7f5/423dc4e4/<library>...) at the root of the zip, the
// format of the WCM data module and of export-wcm-data with several libraries.
func dxConnectToZip(ctx context.Context, tarPath, zipPath string) (int64, error) {
	raw := zipPath + ".raw"
	merged := zipPath + ".merged"
	defer os.RemoveAll(raw)    //nolint:errcheck
	defer os.RemoveAll(merged) //nolint:errcheck
	archive, err := sniffArchive(tarPath)
	if err != nil {
		return 0, err
	}
	if archive != tarPath {
		defer os.Remove(archive) //nolint:errcheck
	}
	if _, err := source.ExtractArchiveProgressContext(ctx, archive, raw, nil); err != nil {
		return 0, fatal(reasonExportFailed, "the archive of DXConnect cannot be read: %v", err)
	}
	tops, err := os.ReadDir(raw)
	if err != nil {
		return 0, err
	}
	libs := 0
	for _, t := range tops {
		if !t.IsDir() {
			continue
		}
		inner, err := os.ReadDir(filepath.Join(raw, t.Name()))
		if err != nil {
			return 0, err
		}
		for _, l := range inner {
			if l.IsDir() {
				if err := dx.MergeTree(filepath.Join(raw, t.Name(), l.Name()), merged); err != nil {
					return 0, err
				}
				libs++
			}
		}
	}
	if libs == 0 {
		return 0, fatal(reasonExportFailed, "the archive of DXConnect has no library folder")
	}
	if _, err := dx.ZipDir(merged, zipPath); err != nil {
		return 0, err
	}
	fi, err := os.Stat(zipPath)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

// dxConnectTar packs libraries of an export for DXConnect import-wcm-data: WCM_Library-<time>/<library>/<JCR export>,
// one folder per library, as a plain tar named .tar.gz (the form DXConnect itself exports).
func dxConnectTar(exp *wcmexport.Export, libs []wcmexport.Library, work string) (string, error) {
	stamp := time.Now().UTC().Format("02012006150405")
	stage := filepath.Join(work, "dxconnect-stage")
	_ = os.RemoveAll(stage)
	defer os.RemoveAll(stage) //nolint:errcheck
	for i, l := range libs {
		z := filepath.Join(work, fmt.Sprintf("dxconnect-lib-%02d.zip", i))
		if _, err := exp.WriteZip([]wcmexport.Library{l}, z); err != nil {
			return "", err
		}
		dir := filepath.Join(stage, "WCM_Library-"+stamp, strings.ToLower(unsafeChars.ReplaceAllString(l.Title, "-")))
		_, err := source.ExtractArchive(z, dir)
		_ = os.Remove(z)
		if err != nil {
			return "", err
		}
	}
	out := filepath.Join(work, "WCM_Library-"+stamp+".tar.gz")
	return out, dx.TarDir(stage, out)
}

// check reads the downloaded zip: the libraries found in it (sorted by title).
func (j wcmExportJob) check(ctx context.Context, n int64) (int64, []dxv1.WCMExportedLibrary, error) {
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

// sniffArchive gives the archive of DXConnect the extension of its real format. DXConnect names it .tar.gz, but the
// container Core sends a plain tar and a traditional DX on a VM may send a gzip or a zip: the content decides, not the
// name. It returns the path to extract (a hard link or copy with the right extension, or the same path for a tar).
func sniffArchive(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	f.Close() //nolint:errcheck
	head = head[:n]
	ext := ""
	switch {
	case len(head) >= 4 && head[0] == 'P' && head[1] == 'K' && head[2] == 3 && head[3] == 4:
		ext = ".zip"
	case len(head) >= 2 && head[0] == 0x1f && head[1] == 0x8b:
		ext = ".tar.gz"
	case len(head) >= 3 && head[0] == 'B' && head[1] == 'Z' && head[2] == 'h':
		ext = ".tar.bz2"
	case len(head) >= 262 && string(head[257:262]) == "ustar":
		return path, nil
	default:
		return "", fatal(reasonExportFailed, "the archive of DXConnect is not a tar, tar.gz, tar.bz2 or zip (first bytes %x)", head[:min(len(head), 8)])
	}
	out := path + ext
	if err := os.Rename(path, out); err != nil {
		return "", err
	}
	return out, nil
}
