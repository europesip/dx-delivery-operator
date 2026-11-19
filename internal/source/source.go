// Package source gets the files of a Script Application or of a DAM collection: from an OCI image (read
// directly from the registry, nothing runs in the cluster), from a ConfigMap or from an archive URL. It
// prepares a working copy with the substitutions applied, and computes the hashes the operator compares.
package source

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dxv1 "github.com/europesip/hcl-dx-scriptapp-operator/api/v1alpha1"
)

// MaxFileSize: WCM stores each file of a Script Application in one element of at most 5,000,000 bytes; a
// bigger file makes the push fail half way and leaves an incomplete item. Files are checked before any push.
const MaxFileSize = 4_500_000

// File of a source.
type File struct {
	Path   string // relative, with "/"
	Size   int64
	SHA256 string
}

// Tree is the files of a source in a local folder.
type Tree struct {
	Dir      string
	Revision string // what identifies this version of the source (image digest, ConfigMap hash, archive SHA256)
	Files    []File
}

// Fetcher gets sources and keeps a cache by revision in CacheDir.
type Fetcher struct {
	Client   client.Client
	CacheDir string
	mu       sync.Mutex
}

// Fetch gets the files of a source.
func (f *Fetcher) Fetch(ctx context.Context, namespace string, src dxv1.Source) (*Tree, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := os.MkdirAll(f.CacheDir, 0o700); err != nil {
		return nil, err
	}
	switch {
	case src.Image != nil:
		return f.fromImage(ctx, namespace, src.Image)
	case src.ConfigMap != nil:
		return f.fromConfigMap(ctx, namespace, src.ConfigMap)
	case src.URL != nil:
		return f.fromURL(ctx, src.URL)
	}
	return nil, errors.New("the source has no image, configMap or url")
}

func shortHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])[:16]
}

// cached returns the tree of a revision already extracted.
func (f *Fetcher) cached(rev string) (*Tree, bool) {
	dir := filepath.Join(f.CacheDir, shortHash(rev))
	if _, err := os.Stat(filepath.Join(dir, ".complete")); err != nil {
		return nil, false
	}
	t, err := scan(dir, rev)
	return t, err == nil
}

func (f *Fetcher) newDir(rev string) (string, error) {
	dir := filepath.Join(f.CacheDir, shortHash(rev))
	_ = os.RemoveAll(dir)
	return dir, os.MkdirAll(dir, 0o700)
}

func finish(dir, rev string) (*Tree, error) {
	if err := os.WriteFile(filepath.Join(dir, ".complete"), []byte(rev), 0o600); err != nil {
		return nil, err
	}
	return scan(dir, rev)
}

// scan lists the files of a folder (without the operator's marker file).
func scan(dir, rev string) (*Tree, error) {
	t := &Tree{Dir: dir, Revision: rev}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if rel == ".complete" {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		h := sha256.Sum256(data)
		t.Files = append(t.Files, File{Path: rel, Size: int64(len(data)), SHA256: hex.EncodeToString(h[:])})
		return nil
	})
	sort.Slice(t.Files, func(i, j int) bool { return t.Files[i].Path < t.Files[j].Path })
	return t, err
}

// safeJoin joins a relative path to dir, refusing paths that leave it.
func safeJoin(dir, rel string) (string, error) {
	clean := path.Clean("/" + filepath.ToSlash(rel))
	if clean == "/" {
		return "", errors.New("empty path")
	}
	return filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(clean, "/"))), nil
}

// ---------------------------------------------------------------------------------------------- image

// keychain resolves registry credentials from dockerconfigjson Secrets.
type keychain struct{ auths map[string]authn.AuthConfig }

func (k keychain) Resolve(r authn.Resource) (authn.Authenticator, error) {
	for _, key := range []string{r.RegistryStr(), "https://" + r.RegistryStr(), "http://" + r.RegistryStr()} {
		if a, ok := k.auths[key]; ok {
			return authn.FromConfig(a), nil
		}
	}
	return authn.Anonymous, nil
}

func (f *Fetcher) keychain(ctx context.Context, namespace string, refs []dxv1.LocalObjectReference) (authn.Keychain, error) {
	k := keychain{auths: map[string]authn.AuthConfig{}}
	for _, r := range refs {
		var s corev1.Secret
		if err := f.Client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: r.Name}, &s); err != nil {
			return nil, fmt.Errorf("pull secret %s: %w", r.Name, err)
		}
		var cfg struct {
			Auths map[string]authn.AuthConfig `json:"auths"`
		}
		if err := json.Unmarshal(s.Data[corev1.DockerConfigJsonKey], &cfg); err != nil {
			return nil, fmt.Errorf("pull secret %s is not a dockerconfigjson: %w", r.Name, err)
		}
		for reg, a := range cfg.Auths {
			k.auths[reg] = a
		}
	}
	return k, nil
}

func (f *Fetcher) fromImage(ctx context.Context, namespace string, src *dxv1.ImageSource) (*Tree, error) {
	ref, err := name.ParseReference(src.Ref)
	if err != nil {
		return nil, fmt.Errorf("image reference %q: %w", src.Ref, err)
	}
	kc, err := f.keychain(ctx, namespace, src.PullSecrets)
	if err != nil {
		return nil, err
	}
	img, err := remote.Image(ref, remote.WithContext(ctx), remote.WithAuthFromKeychain(kc), remote.WithPlatform(v1.Platform{OS: "linux", Architecture: "amd64"}))
	if err != nil {
		return nil, fmt.Errorf("image %s: %w", src.Ref, err)
	}
	digest, err := img.Digest()
	if err != nil {
		return nil, err
	}
	sub := strings.Trim(path.Clean("/"+src.Path), "/") // path inside the image, e.g. "app"
	rev := ref.Context().String() + "@" + digest.String() + ":" + sub
	if t, ok := f.cached(rev); ok {
		return t, nil
	}
	dir, err := f.newDir(rev)
	if err != nil {
		return nil, err
	}
	// An OCI artifact pushed with ORAS (oras push <ref> file...): its layers are the files themselves, named by the
	// org.opencontainers.image.title annotation, not tar layers of a filesystem. They are written as they are (for
	// example the .tar.bz2 of a WCM library export, unpacked later by whoever reads the tree).
	if m, merr := img.Manifest(); merr == nil && isFileArtifact(m) {
		n, err := writeArtifactFiles(img, m, dir)
		if err != nil {
			return nil, fmt.Errorf("artifact %s: %w", src.Ref, err)
		}
		if n == 0 {
			return nil, fmt.Errorf("artifact %s has no files", src.Ref)
		}
		return finish(dir, rev)
	}
	rc := mutate.Extract(img) // flattened filesystem, whiteouts applied
	n, err := untar(rc, dir, sub)
	_ = rc.Close()
	if err != nil {
		return nil, fmt.Errorf("image %s: %w", src.Ref, err)
	}
	if n > 0 {
		return finish(dir, rev)
	}
	// Nothing under the folder asked for: look at which top-level folders of the image have files. With the default
	// folder (/app) and only one folder that is not part of an operating system (for example /export of a WCM export
	// image), that one is used; otherwise the error names the folders, and it is not retried (NoFilesError).
	cands, err := topLevelFolders(img)
	if err != nil {
		return nil, fmt.Errorf("image %s: %w", src.Ref, err)
	}
	if sub == DefaultImageFolder && len(cands) == 1 {
		alt := cands[0]
		rc2 := mutate.Extract(img)
		n2, err := untar(rc2, dir, alt)
		_ = rc2.Close()
		if err != nil {
			return nil, fmt.Errorf("image %s: %w", src.Ref, err)
		}
		if n2 > 0 {
			return finish(dir, rev)
		}
	}
	return nil, &NoFilesError{Source: "image " + src.Ref, Folder: "/" + sub, Candidates: cands}
}

// DefaultImageFolder is the folder of an image source when the spec gives none (the CRD default /app, without slash).
const DefaultImageFolder = "app"

// NoFilesError: the source was read, but the folder asked for has no files. Retrying cannot fix it (the spec must
// change), so controllers end the run as failed. Candidates are the top-level folders that do have files.
type NoFilesError struct {
	Source     string
	Folder     string
	Candidates []string
}

func (e *NoFilesError) Error() string {
	msg := fmt.Sprintf("%s has no files under %s", e.Source, e.Folder)
	if len(e.Candidates) > 0 {
		c := make([]string, len(e.Candidates))
		for i, x := range e.Candidates {
			c[i] = "/" + x
		}
		msg += "; folders with files: " + strings.Join(c, ", ") + " (set the folder of the source to one of them)"
	}
	return msg
}

// osFolders are the top-level folders of a base image (operating system), never the files of a source.
var osFolders = map[string]bool{
	"bin": true, "boot": true, "dev": true, "etc": true, "home": true, "lib": true, "lib32": true, "lib64": true,
	"libx32": true, "media": true, "mnt": true, "opt": true, "proc": true, "root": true, "run": true, "sbin": true,
	"srv": true, "sys": true, "tmp": true, "usr": true, "var": true, "nix": true,
}

// topLevelFolders lists, sorted, the top-level folders of the image that have regular files, without the folders of
// an operating system.
func topLevelFolders(img v1.Image) ([]string, error) {
	rc := mutate.Extract(img)
	defer rc.Close() //nolint:errcheck
	return foldersWithFiles(rc)
}

func foldersWithFiles(r io.Reader) ([]string, error) {
	tr := tar.NewReader(r)
	seen := map[string]bool{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		p := strings.TrimPrefix(path.Clean("/"+h.Name), "/")
		i := strings.Index(p, "/")
		if i <= 0 {
			continue // a file at the root
		}
		if top := p[:i]; !osFolders[top] {
			seen[top] = true
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

const titleAnnotation = "org.opencontainers.image.title"

// isFileArtifact tells an ORAS file artifact (layers annotated with a file name) from a container image.
func isFileArtifact(m *v1.Manifest) bool {
	if len(m.Layers) == 0 {
		return false
	}
	for _, l := range m.Layers {
		if l.Annotations[titleAnnotation] == "" {
			return false
		}
	}
	return true
}

// writeArtifactFiles writes each layer of an ORAS artifact to dir/<title>. Folders pushed by ORAS (annotation
// io.deis.oras.content.unpack=true, a tar+gzip of the folder) are unpacked as "oras pull" does: their entries already
// start with the folder name, so they go into dir (dir/<title>/...), not into dir/<title>/<title>/... (#41).
func writeArtifactFiles(img v1.Image, m *v1.Manifest, dir string) (int, error) {
	n := 0
	for _, d := range m.Layers {
		title := d.Annotations[titleAnnotation]
		dst, err := safeJoin(dir, title)
		if err != nil {
			return n, fmt.Errorf("file name %q: %w", title, err)
		}
		layer, err := img.LayerByDigest(d.Digest)
		if err != nil {
			return n, err
		}
		rc, err := layer.Compressed() // the blob as stored: the file itself
		if err != nil {
			return n, err
		}
		if d.Annotations["io.deis.oras.content.unpack"] == "true" {
			gz, gerr := gzip.NewReader(rc)
			if gerr != nil {
				_ = rc.Close()
				return n, gerr
			}
			k, uerr := untar(gz, dir, "")
			_ = rc.Close()
			if uerr != nil {
				return n, uerr
			}
			n += k
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			_ = rc.Close()
			return n, err
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			_ = rc.Close()
			return n, err
		}
		_, err = io.Copy(out, io.LimitReader(rc, MaxDownloadBytes))
		_ = out.Close()
		_ = rc.Close()
		if err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// IsArchive tells whether a file name is an archive that ExtractArchive unpacks.
func IsArchive(name string) bool {
	l := strings.ToLower(name)
	for _, ext := range []string{".zip", ".tar", ".tar.gz", ".tgz", ".tar.bz2", ".tbz2", ".tbz"} {
		if strings.HasSuffix(l, ext) {
			return true
		}
	}
	return false
}

// ExtractArchive unpacks a .zip, .tar, .tar.gz/.tgz or .tar.bz2 file into dir and returns the number of files.
func ExtractArchive(file, dir string) (int, error) { return ExtractArchiveProgress(file, dir, nil) }

// Progress of an unpack: how much of the archive has been read (of Total bytes) and the files written so far.
type Progress struct{ Read, Total int64 }

// countingReader counts the bytes read from the archive and calls report at most every 15 seconds.
type countingReader struct {
	ctx    context.Context
	r      io.Reader
	n, tot int64
	last   time.Time
	report func(Progress)
}

func (c *countingReader) Read(p []byte) (int, error) {
	if c.ctx != nil && c.ctx.Err() != nil { // the run was cancelled (its resource deleted): stop unpacking
		return 0, c.ctx.Err()
	}
	n, err := c.r.Read(p)
	c.n += int64(n)
	if c.report != nil && time.Since(c.last) >= 15*time.Second {
		c.last = time.Now()
		c.report(Progress{Read: c.n, Total: c.tot})
	}
	return n, err
}

// ExtractArchiveProgress is ExtractArchive with a report of the progress (nil: none), so a long unpack shows that it
// moves on.
func ExtractArchiveProgress(file, dir string, report func(Progress)) (int, error) {
	return ExtractArchiveProgressContext(context.Background(), file, dir, report)
}

// ExtractArchiveProgressContext is ExtractArchiveProgress that stops when ctx is cancelled.
func ExtractArchiveProgressContext(ctx context.Context, file, dir string, report func(Progress)) (int, error) {
	f0, err := os.Open(file)
	if err != nil {
		return 0, err
	}
	defer f0.Close() //nolint:errcheck
	fi0, err := f0.Stat()
	if err != nil {
		return 0, err
	}
	cr := &countingReader{ctx: ctx, r: f0, tot: fi0.Size(), last: time.Now(), report: report}
	l := strings.ToLower(file)
	switch {
	case strings.HasSuffix(l, ".zip"):
		return unzipFile(f0, fi0.Size(), dir, "")
	case strings.HasSuffix(l, ".tar.bz2") || strings.HasSuffix(l, ".tbz2") || strings.HasSuffix(l, ".tbz"):
		return untar(bzip2.NewReader(bufio.NewReaderSize(cr, 1<<20)), dir, "")
	case strings.HasSuffix(l, ".tar"):
		return untar(bufio.NewReaderSize(cr, 1<<20), dir, "")
	default:
		gz, err := gzip.NewReader(bufio.NewReaderSize(cr, 1<<20))
		if err != nil {
			return 0, fmt.Errorf("%s is not a .tar.gz, .tar.bz2, .tar or .zip archive", filepath.Base(file))
		}
		return untar(gz, dir, "")
	}
}

// untar extracts the regular files under prefix (a path without leading slash, "" for all) into dir.
func untar(r io.Reader, dir, prefix string) (int, error) {
	tr := tar.NewReader(r)
	n := 0
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return n, nil
		}
		if err != nil {
			return n, err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		p := strings.TrimPrefix(path.Clean("/"+h.Name), "/")
		if prefix != "" {
			if !strings.HasPrefix(p, prefix+"/") {
				continue
			}
			p = strings.TrimPrefix(p, prefix+"/")
		}
		dst, err := safeJoin(dir, p)
		if err != nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return n, err
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return n, err
		}
		if _, err := io.Copy(out, io.LimitReader(tr, 200<<20)); err != nil {
			_ = out.Close()
			return n, err
		}
		_ = out.Close()
		n++
	}
}

// ---------------------------------------------------------------------------------------------- ConfigMap

func (f *Fetcher) fromConfigMap(ctx context.Context, namespace string, src *dxv1.ConfigMapSource) (*Tree, error) {
	var cm corev1.ConfigMap
	if err := f.Client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: src.Name}, &cm); err != nil {
		return nil, fmt.Errorf("ConfigMap %s: %w", src.Name, err)
	}
	files := map[string][]byte{}
	for k, v := range cm.Data {
		files[k] = []byte(v)
	}
	for k, v := range cm.BinaryData {
		files[k] = v
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("ConfigMap %s is empty", src.Name)
	}
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		fmt.Fprintf(h, "%s\x00%d\x00", k, len(files[k]))
		h.Write(files[k])
	}
	rev := "configmap:" + src.Name + "@sha256:" + hex.EncodeToString(h.Sum(nil))
	if t, ok := f.cached(rev); ok {
		return t, nil
	}
	dir, err := f.newDir(rev)
	if err != nil {
		return nil, err
	}
	for _, k := range keys {
		dst, err := safeJoin(dir, strings.ReplaceAll(k, "__", "/"))
		if err != nil {
			return nil, fmt.Errorf("ConfigMap key %q: %w", k, err)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(dst, files[k], 0o600); err != nil {
			return nil, err
		}
	}
	return finish(dir, rev)
}

// ---------------------------------------------------------------------------------------------- URL

// MaxDownloadBytes limits the archives downloaded from a URL (WCM library exports are often hundreds of MB).
const MaxDownloadBytes = 8 << 30

func (f *Fetcher) fromURL(ctx context.Context, src *dxv1.URLSource) (*Tree, error) {
	sub := strings.Trim(path.Clean("/"+src.Path), "/")
	if src.SHA256 != "" {
		if t, ok := f.cached("url@sha256:" + strings.ToLower(src.SHA256) + ":" + sub); ok {
			return t, nil
		}
	}
	cctx, cancel := context.WithTimeout(ctx, 60*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, src.URL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", src.URL, err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: HTTP %d", src.URL, resp.StatusCode)
	}
	// The archive goes to a temporary file (streamed, hashed on the way), never into memory.
	tmp, err := os.CreateTemp(f.CacheDir, "download-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck
	defer tmp.Close()           //nolint:errcheck
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, MaxDownloadBytes+1))
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", src.URL, err)
	}
	if n > MaxDownloadBytes {
		return nil, fmt.Errorf("download %s: bigger than %d bytes", src.URL, int64(MaxDownloadBytes))
	}
	got := hex.EncodeToString(h.Sum(nil))
	if src.SHA256 != "" && !strings.EqualFold(src.SHA256, got) {
		return nil, fmt.Errorf("download %s: SHA256 is %s, expected %s", src.URL, got, src.SHA256)
	}
	rev := "url@sha256:" + got + ":" + sub
	if t, ok := f.cached(rev); ok {
		return t, nil
	}
	dir, err := f.newDir(rev)
	if err != nil {
		return nil, err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	lower := strings.ToLower(strings.SplitN(src.URL, "?", 2)[0])
	var files int
	switch {
	case strings.HasSuffix(lower, ".zip"):
		files, err = unzipFile(tmp, n, dir, sub)
	case strings.HasSuffix(lower, ".tar.bz2") || strings.HasSuffix(lower, ".tbz2") || strings.HasSuffix(lower, ".tbz"):
		files, err = untar(bzip2.NewReader(bufio.NewReaderSize(tmp, 1<<20)), dir, sub)
	case strings.HasSuffix(lower, ".tar"):
		files, err = untar(bufio.NewReaderSize(tmp, 1<<20), dir, sub)
	default: // .tar.gz / .tgz
		gz, gerr := gzip.NewReader(bufio.NewReaderSize(tmp, 1<<20))
		if gerr != nil {
			return nil, fmt.Errorf("%s is not a .tar.gz, .tar.bz2, .tar or .zip archive", src.URL)
		}
		files, err = untar(gz, dir, sub)
	}
	if err != nil {
		return nil, err
	}
	if files == 0 {
		return nil, &NoFilesError{Source: "archive " + src.URL, Folder: "/" + sub}
	}
	return finish(dir, rev)
}

func unzipFile(r io.ReaderAt, size int64, dir, prefix string) (int, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return 0, err
	}
	return unzipReader(zr, dir, prefix)
}

func unzip(data []byte, dir, prefix string) (int, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return 0, err
	}
	return unzipReader(zr, dir, prefix)
}

func unzipReader(zr *zip.Reader, dir, prefix string) (int, error) {
	n := 0
	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() {
			continue
		}
		p := strings.TrimPrefix(path.Clean("/"+zf.Name), "/")
		if prefix != "" {
			if !strings.HasPrefix(p, prefix+"/") {
				continue
			}
			p = strings.TrimPrefix(p, prefix+"/")
		}
		dst, err := safeJoin(dir, p)
		if err != nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return n, err
		}
		rc, err := zf.Open()
		if err != nil {
			return n, err
		}
		b, err := io.ReadAll(io.LimitReader(rc, 200<<20))
		_ = rc.Close()
		if err != nil {
			return n, err
		}
		if err := os.WriteFile(dst, b, 0o600); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
