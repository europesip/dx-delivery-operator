// Package wcmexport reads a Web Content Manager library export in JCR format (the tree that ConfigEngine
// export-wcm-data or the WCM data module write: <root>/<ws>/<library>.node plus a <library>/ folder per library) and
// packs a selection of its libraries into zip files that DX Compose accepts (dxFileTransfer: at most 500 MB per upload,
// file names without spaces).
package wcmexport

import (
	"archive/zip"
	"bufio"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Library is one library of the export.
type Library struct {
	// Key is the name of its .node file without the extension (a hash written by the exporter, e.g. 9d0b88ba).
	Key   string
	Title string
	// UUID of the library node (icm:uuid of the .node file).
	UUID string
	// Bytes and Files: size of the library on disk (its .node file and its folder), before compression.
	Bytes int64
	Files int
	// UnsafePaths: files or folders of the library whose names dxFileTransfer rejects (see UnsafeName). DX derives the
	// folder of a node's values from the node name, so they cannot be renamed: such a library cannot be uploaded.
	UnsafePaths []string
}

// Export is a library export found in a folder.
type Export struct {
	Root string // the folder given to Open
	// WS is the folder, relative to Root, that holds the <library>.node files (for example 554ee7f5/423dc4e4).
	WS        string
	Libraries []Library
}

var (
	libraryMarker = []byte(`sv:name="icm:libraries[`)
	titleRe       = regexp.MustCompile(`<sv:property sv:name="icm:title"[^>]*>\s*<sv:value>([^<]*)</sv:value>`)
	uuidRe        = regexp.MustCompile(`icm:uuid="([^"]+)"`)
)

// PortalSiteUUID is the fixed ID of the Portal Site library (the managed pages of the portal) in every DX: base64 of
// "Managed Pages Root Library".
const PortalSiteUUID = "TWFuYWdlZCBQYWdlcyBSb290IExpYnJhcnk"

// PortalSiteTitle is its usual title.
const PortalSiteTitle = "Portal Site"

// IsPortalSite tells whether a library is Portal Site, by its fixed ID or, failing that, by its title.
func (l Library) IsPortalSite() bool {
	return l.UUID == PortalSiteUUID || strings.EqualFold(strings.TrimSpace(l.Title), PortalSiteTitle)
}

// HCLDefaultLibraries are the libraries that HCL DX ships or creates by itself (system, templates and samples).
var HCLDefaultLibraries = []string{
	"Web Content", "Portal Site", "Script Portlet Library", "Web Content Templates 3.0", "Template Page Content 3.0",
	"Web Resources v70", "Blog Template v70", "Blog Solo Template v70", "Wiki Template v70", "Social Lists 1.0",
	"WCM Comments 1.0", "ML Configuration", "Site Builder Template Library", "Woodburn Studio Content",
	"Woodburn Studio Content Spanish", "Woodburn Studio Content French", "Woodburn Studio Design",
	"Blueprint Design System", "Blueprint Design Library", "Blueprint Taxonomies", "Blueprint Common Resources",
	"Blueprint Web Content", "Nex Haven Page Components", "Content Templates 3.0",
}

// Open finds the libraries of an export under root (it looks up to four levels deep for the folder of .node files
// whose root nodes are libraries).
func Open(root string) (*Export, error) {
	var ws string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if rel != "." && strings.Count(rel, string(filepath.Separator)) >= 4 {
			return filepath.SkipDir
		}
		entries, err := os.ReadDir(p)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".node") {
				continue
			}
			if ok, _ := fileContains(filepath.Join(p, e.Name()), libraryMarker, 8192); ok {
				ws = p
				return fs.SkipAll
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if ws == "" {
		return nil, errors.New("no WCM library export found: no folder with <library>.node files (export-wcm-data / WCM data module format)")
	}
	exp := &Export{Root: root}
	exp.WS, _ = filepath.Rel(root, ws)
	entries, err := os.ReadDir(ws)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".node") {
			continue
		}
		p := filepath.Join(ws, e.Name())
		head, err := readHead(p, 1<<20)
		if err != nil {
			return nil, err
		}
		if !strings.Contains(head, string(libraryMarker)) {
			continue
		}
		m := titleRe.FindStringSubmatch(head)
		if m == nil {
			return nil, fmt.Errorf("library %s has no icm:title", e.Name())
		}
		lib := Library{Key: strings.TrimSuffix(e.Name(), ".node"), Title: html.UnescapeString(strings.TrimSpace(m[1]))}
		if u := uuidRe.FindStringSubmatch(head); u != nil {
			lib.UUID = u[1]
		}
		info, _ := e.Info()
		lib.Bytes, lib.Files = info.Size(), 1
		_ = filepath.WalkDir(filepath.Join(ws, lib.Key), func(p string, d fs.DirEntry, err error) error {
			if err == nil && UnsafeName(d.Name()) {
				rel, _ := filepath.Rel(root, p)
				lib.UnsafePaths = append(lib.UnsafePaths, filepath.ToSlash(rel))
			}
			if err == nil && !d.IsDir() {
				if fi, ierr := d.Info(); ierr == nil {
					lib.Bytes += fi.Size()
				}
				lib.Files++
			}
			return nil
		})
		exp.Libraries = append(exp.Libraries, lib)
	}
	sort.Slice(exp.Libraries, func(i, j int) bool { return exp.Libraries[i].Title < exp.Libraries[j].Title })
	if len(exp.Libraries) == 0 {
		return nil, errors.New("the export has no libraries")
	}
	return exp, nil
}

func readHead(p string, n int) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close() //nolint:errcheck
	b, err := io.ReadAll(io.LimitReader(f, int64(n)))
	return string(b), err
}

func fileContains(p string, needle []byte, n int) (bool, error) {
	s, err := readHead(p, n)
	return strings.Contains(s, string(needle)), err
}

// Titles of a list of libraries.
func Titles(libs []Library) []string {
	out := make([]string, len(libs))
	for i, l := range libs {
		out[i] = l.Title
	}
	return out
}

// Select applies include, exclude and excludeHCLDefaults (titles compared ignoring case). A title of include that is
// not in the export is an error. It returns the selected libraries and the ones left out with the reason.
func Select(libs []Library, include, exclude []string, excludeHCLDefaults bool) (selected []Library, left map[string]string, err error) {
	byTitle := map[string]Library{}
	for _, l := range libs {
		byTitle[strings.ToLower(l.Title)] = l
	}
	left = map[string]string{}
	if len(include) > 0 {
		var missing []string
		seen := map[string]bool{}
		for _, t := range include {
			l, ok := byTitle[strings.ToLower(strings.TrimSpace(t))]
			if !ok {
				missing = append(missing, t)
				continue
			}
			if !seen[l.Key] {
				seen[l.Key] = true
				selected = append(selected, l)
			}
		}
		if len(missing) > 0 {
			return nil, nil, fmt.Errorf("libraries.include names libraries that are not in the export: %s", strings.Join(missing, ", "))
		}
		for _, l := range libs {
			if !seen[l.Key] {
				left[l.Title] = "not in libraries.include"
			}
		}
	} else {
		selected = append(selected, libs...)
	}
	drop := map[string]string{}
	for _, t := range exclude {
		drop[strings.ToLower(strings.TrimSpace(t))] = "in libraries.exclude"
	}
	if excludeHCLDefaults {
		for _, t := range HCLDefaultLibraries {
			k := strings.ToLower(t)
			if _, ok := drop[k]; !ok {
				drop[k] = "library shipped by HCL DX (libraries.excludeHCLDefaults)"
			}
		}
	}
	out := selected[:0]
	for _, l := range selected {
		if why, ok := drop[strings.ToLower(l.Title)]; ok {
			left[l.Title] = why
			continue
		}
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Title < out[j].Title })
	return out, left, nil
}

// Chunk packs libraries into groups whose size on disk is at most maxBytes and whose number of files is at most
// maxFiles (0 = no file limit). The zip is smaller than the size on disk, so the upload limit holds; the file limit keeps
// each import request short (DX imports ~60 files/s for new items, ~7 files/s when it updates existing ones, and the
// HAProxy of DX cuts requests after 20 minutes). A library bigger than a limit goes alone in its group.
func Chunk(libs []Library, maxBytes int64, maxFiles int) [][]Library {
	sorted := append([]Library(nil), libs...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Bytes > sorted[j].Bytes })
	type bin struct {
		size  int64
		files int
		libs  []Library
	}
	var bins []*bin
	for _, l := range sorted {
		placed := false
		for _, b := range bins {
			if b.size+l.Bytes <= maxBytes && (maxFiles <= 0 || b.files+l.Files <= maxFiles) {
				b.size += l.Bytes
				b.files += l.Files
				b.libs = append(b.libs, l)
				placed = true
				break
			}
		}
		if !placed {
			bins = append(bins, &bin{size: l.Bytes, files: l.Files, libs: []Library{l}})
		}
	}
	out := make([][]Library, len(bins))
	for i, b := range bins {
		out[i] = b.libs
	}
	return out
}

// UnsafeName tells whether dxFileTransfer rejects a file or folder name ("File name contains invalid characters", seen on
// DX Compose CF239 with a space; it also refuses to create such folders). Whitespace, control characters and the
// characters that are invalid in file names on Windows are treated as unsafe.
func UnsafeName(name string) bool {
	for _, r := range name {
		if r <= ' ' || r == 0x7f || strings.ContainsRune(`\:*?"<>|`, r) {
			return true
		}
	}
	return false
}

// ZipResult is a zip written by WriteZip.
type ZipResult struct {
	Path  string
	Bytes int64
	Files int
}

// WriteZip writes the given libraries (they must have no UnsafePaths) (their .node file and their folder, under the same WS path) into a new zip file.
func (e *Export) WriteZip(libs []Library, out string) (*ZipResult, error) {
	f, err := os.Create(out)
	if err != nil {
		return nil, err
	}
	bw := bufio.NewWriterSize(f, 1<<20)
	zw := zip.NewWriter(bw)
	res := &ZipResult{Path: out}
	add := func(abs string) error {
		rel, err := filepath.Rel(e.Root, abs)
		if err != nil {
			return err
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: filepath.ToSlash(rel), Method: zip.Deflate})
		if err != nil {
			return err
		}
		src, err := os.Open(abs)
		if err != nil {
			return err
		}
		_, err = io.Copy(w, src)
		_ = src.Close()
		res.Files++
		return err
	}
	ws := filepath.Join(e.Root, e.WS)
	for _, l := range libs {
		if err := add(filepath.Join(ws, l.Key+".node")); err != nil {
			_ = f.Close()
			return nil, err
		}
		dir := filepath.Join(ws, l.Key)
		if _, err := os.Stat(dir); err != nil {
			continue // a library without items has only its .node file
		}
		if err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			return add(p)
		}); err != nil {
			_ = f.Close()
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := bw.Flush(); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	fi, err := os.Stat(out)
	if err != nil {
		return nil, err
	}
	res.Bytes = fi.Size()
	return res, nil
}
