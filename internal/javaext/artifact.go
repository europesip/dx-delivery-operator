// SPDX-License-Identifier: MIT

/*
Copyright 2026 EUROPESIP.

Licensed under the MIT License (see LICENSE).
*/

// Package javaext holds what the DXJavaExtension controller, its admission webhook and the init container of the
// Web Engine share (issue #54): reading the artifact, unpacking it, portlet.xml, the Liberty override, the
// XMLAccess registration and the pod injection.
package javaext

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
)

// MaxArtifactBytes: the largest file accepted in an artifact (a WAR or JAR, not an image).
const MaxArtifactBytes = 512 << 20

// Artifact annotations written by our build (also read by the controller).
const (
	AnnotationKind        = "com.europesip.dx.kind" // java-extension
	AnnotationJavaType    = "com.europesip.dx.java.type"
	AnnotationJSR         = "com.europesip.dx.java.jsr"
	AnnotationContextRoot = "com.europesip.dx.java.context-root"
	AnnotationPortletUIDs = "com.europesip.dx.java.portlet-uids"
	AnnotationWebAppUID   = "com.europesip.dx.java.webapp-uid"
	AnnotationMinCF       = "com.europesip.dx.java.min-cf"
	annotationTitle       = "org.opencontainers.image.title"
)

// ErrNotFound: the artifact (tag or digest) does not exist in the registry.
var ErrNotFound = errors.New("artifact not found in the registry")

// Fetched is an artifact read from the registry.
type Fetched struct {
	// Digest of the manifest (sha256:...).
	Digest string
	// Repository without tag or digest.
	Repository string
	// Annotations of the manifest.
	Annotations map[string]string
	// FileName of the file in the artifact (annotation org.opencontainers.image.title of its layer).
	FileName string
	// Data of the file.
	Data []byte
}

// PinnedRef is repository@digest.
func (f *Fetched) PinnedRef() string { return f.Repository + "@" + f.Digest }

// Resolve returns the digest of a reference (a tag is resolved, a digest is checked). ErrNotFound when it does
// not exist.
func Resolve(ctx context.Context, ref string, kc authn.Keychain) (string, string, error) {
	r, err := name.ParseReference(ref)
	if err != nil {
		return "", "", fmt.Errorf("artifact reference %q: %w", ref, err)
	}
	desc, err := remote.Head(r, remote.WithContext(ctx), remote.WithAuthFromKeychain(kc))
	if err != nil {
		if notFound(err) {
			return "", "", fmt.Errorf("%s: %w", ref, ErrNotFound)
		}
		return "", "", fmt.Errorf("artifact %s: %w", ref, err)
	}
	return desc.Digest.String(), r.Context().Name(), nil
}

func notFound(err error) bool {
	var te *transport.Error
	if errors.As(err, &te) {
		if te.StatusCode == 404 {
			return true
		}
		for _, d := range te.Errors {
			if d.Code == transport.ManifestUnknownErrorCode || d.Code == transport.NameUnknownErrorCode {
				return true
			}
		}
	}
	return false
}

// Fetch reads an artifact: its manifest (annotations) and its first file layer, verifying both digests.
func Fetch(ctx context.Context, ref string, kc authn.Keychain) (*Fetched, error) {
	r, err := name.ParseReference(ref)
	if err != nil {
		return nil, fmt.Errorf("artifact reference %q: %w", ref, err)
	}
	desc, err := remote.Get(r, remote.WithContext(ctx), remote.WithAuthFromKeychain(kc))
	if err != nil {
		if notFound(err) {
			return nil, fmt.Errorf("%s: %w", ref, ErrNotFound)
		}
		return nil, fmt.Errorf("artifact %s: %w", ref, err)
	}
	sum := sha256.Sum256(desc.Manifest)
	got := "sha256:" + hex.EncodeToString(sum[:])
	if d, ok := r.(name.Digest); ok && d.DigestStr() != got {
		return nil, fmt.Errorf("artifact %s: manifest digest %s does not match", ref, got)
	}
	m, err := v1.ParseManifest(bytes.NewReader(desc.Manifest))
	if err != nil {
		return nil, fmt.Errorf("artifact %s: manifest: %w", ref, err)
	}
	if len(m.Layers) == 0 {
		return nil, fmt.Errorf("artifact %s has no file", ref)
	}
	layer := m.Layers[0]
	for _, l := range m.Layers { // prefer a layer with a file name we know
		t := strings.ToLower(l.Annotations[annotationTitle])
		if strings.HasSuffix(t, ".war") || strings.HasSuffix(t, ".jar") || strings.HasSuffix(t, ".tar") || strings.HasSuffix(t, ".tgz") || strings.HasSuffix(t, ".tar.gz") {
			layer = l
			break
		}
	}
	if layer.Size > MaxArtifactBytes {
		return nil, fmt.Errorf("artifact %s: file of %d bytes is too large", ref, layer.Size)
	}
	blob, err := remote.Layer(r.Context().Digest(layer.Digest.String()), remote.WithContext(ctx), remote.WithAuthFromKeychain(kc))
	if err != nil {
		return nil, fmt.Errorf("artifact %s: file: %w", ref, err)
	}
	rc, err := blob.Compressed()
	if err != nil {
		return nil, fmt.Errorf("artifact %s: file: %w", ref, err)
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, MaxArtifactBytes+1))
	if err != nil {
		return nil, fmt.Errorf("artifact %s: file: %w", ref, err)
	}
	fs := sha256.Sum256(data)
	if "sha256:"+hex.EncodeToString(fs[:]) != layer.Digest.String() {
		return nil, fmt.Errorf("artifact %s: file digest does not match %s", ref, layer.Digest)
	}
	return &Fetched{
		Digest:      got,
		Repository:  r.Context().Name(),
		Annotations: m.Annotations,
		FileName:    layer.Annotations[annotationTitle],
		Data:        data,
	}, nil
}

// format of a file: zip (WAR/JAR), gzip (tar.gz) or tar.
func format(data []byte) string {
	switch {
	case len(data) > 4 && bytes.Equal(data[:4], []byte("PK\x03\x04")):
		return "zip"
	case len(data) > 2 && data[0] == 0x1f && data[1] == 0x8b:
		return "gzip"
	case len(data) > 262 && string(data[257:262]) == "ustar":
		return "tar"
	}
	return ""
}

// entries walks the files of an archive (zip, tar or tar.gz) calling fn with the cleaned path.
func entries(data []byte, fn func(path string, mode os.FileMode, r io.Reader) error) error {
	switch format(data) {
	case "zip":
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return err
		}
		for _, f := range zr.File {
			if f.FileInfo().IsDir() {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return err
			}
			err = fn(f.Name, 0o644, rc)
			rc.Close()
			if err != nil {
				return err
			}
		}
		return nil
	case "gzip", "tar":
		var r io.Reader = bytes.NewReader(data)
		if format(data) == "gzip" {
			gz, err := gzip.NewReader(r)
			if err != nil {
				return err
			}
			defer gz.Close()
			r = gz
		}
		tr := tar.NewReader(r)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			if h.Typeflag != tar.TypeReg {
				continue
			}
			if err := fn(h.Name, 0o644, tr); err != nil {
				return err
			}
		}
	}
	return errors.New("the file is not a WAR/JAR (zip), a tar or a tar.gz")
}

// stripWarRoot: a tar of an expanded WAR may have the files under a top folder (foo.war/WEB-INF/...). It returns
// that folder ("" when WEB-INF is at the root).
func warRoot(paths []string) string {
	for _, p := range paths {
		p = strings.TrimPrefix(filepath.ToSlash(p), "./")
		if i := strings.Index(p, "WEB-INF/"); i >= 0 {
			return p[:i]
		}
	}
	return ""
}

func cleanRel(p string) (string, error) {
	p = strings.TrimPrefix(filepath.ToSlash(p), "./")
	c := filepath.Clean(p)
	if c == "." || strings.HasPrefix(c, "..") || filepath.IsAbs(c) {
		return "", fmt.Errorf("unsafe path %q in the archive", p)
	}
	return c, nil
}

// UnpackWAR writes the files of a WAR (zip) or of a tar/tar.gz of an expanded WAR under dir (dir becomes the
// expanded WAR: dir/WEB-INF/...). It returns the number of files.
func UnpackWAR(data []byte, dir string) (int, error) {
	var paths []string
	if err := entries(data, func(p string, _ os.FileMode, _ io.Reader) error { paths = append(paths, p); return nil }); err != nil {
		return 0, err
	}
	root := warRoot(paths)
	n := 0
	err := entries(data, func(p string, mode os.FileMode, r io.Reader) error {
		p = strings.TrimPrefix(strings.TrimPrefix(filepath.ToSlash(p), "./"), root)
		if p == "" {
			return nil
		}
		rel, err := cleanRel(p)
		if err != nil {
			return err
		}
		out := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, r)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		n++
		return err
	})
	return n, err
}

// WriteJARs writes a JAR (the file itself) or every .jar of a tar/tar.gz into dir. It returns the file names.
func WriteJARs(data []byte, fileName, dir string) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if format(data) == "zip" {
		return []string{fileName}, os.WriteFile(filepath.Join(dir, fileName), data, 0o644)
	}
	var names []string
	err := entries(data, func(p string, mode os.FileMode, r io.Reader) error {
		if !strings.HasSuffix(strings.ToLower(p), ".jar") {
			return nil
		}
		base := filepath.Base(p)
		f, err := os.OpenFile(filepath.Join(dir, base), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, r)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		names = append(names, base)
		return err
	})
	if err == nil && len(names) == 0 {
		err = errors.New("no .jar in the archive")
	}
	return names, err
}

// PortletApp is what DX needs from portlet.xml.
type PortletApp struct {
	// UID of the portlet application: the id attribute of <portlet-app> (DX appends ".webmod" for the web module).
	UID string
	// Portlets: the <portlet-name> of every portlet.
	Portlets []string
}

type portletXML struct {
	ID       string `xml:"id,attr"`
	Portlets []struct {
		Name string `xml:"portlet-name"`
	} `xml:"portlet"`
}

// ReadPortletXML reads WEB-INF/portlet.xml from a WAR (zip) or a tar/tar.gz of an expanded WAR.
func ReadPortletXML(data []byte) (*PortletApp, error) {
	var found []byte
	err := entries(data, func(p string, _ os.FileMode, r io.Reader) error {
		if found == nil && strings.HasSuffix(filepath.ToSlash(p), "WEB-INF/portlet.xml") {
			b, err := io.ReadAll(io.LimitReader(r, 4<<20))
			if err != nil {
				return err
			}
			found = b
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if found == nil {
		return nil, errors.New("WEB-INF/portlet.xml not found: is it a portlet WAR?")
	}
	var px portletXML
	if err := xml.Unmarshal(found, &px); err != nil {
		return nil, fmt.Errorf("WEB-INF/portlet.xml: %w", err)
	}
	app := &PortletApp{UID: strings.TrimSpace(px.ID)}
	for _, p := range px.Portlets {
		if n := strings.TrimSpace(p.Name); n != "" {
			app.Portlets = append(app.Portlets, n)
		}
	}
	sort.Strings(app.Portlets)
	if len(app.Portlets) == 0 {
		return nil, errors.New("WEB-INF/portlet.xml has no <portlet>")
	}
	return app, nil
}

type secretKeychain map[string]authn.AuthConfig

func (k secretKeychain) Resolve(r authn.Resource) (authn.Authenticator, error) {
	if a, ok := k[r.RegistryStr()]; ok {
		return authn.FromConfig(a), nil
	}
	return authn.Anonymous, nil
}

// KeychainFromDockerConfigs builds a keychain from the .dockerconfigjson contents of pull secrets.
func KeychainFromDockerConfigs(configs map[string][]byte) (authn.Keychain, error) {
	k := secretKeychain{}
	for name, b := range configs {
		var cfg struct {
			Auths map[string]authn.AuthConfig `json:"auths"`
		}
		if err := jsonUnmarshal(b, &cfg); err != nil {
			return nil, fmt.Errorf("pull secret %s is not a dockerconfigjson: %w", name, err)
		}
		for reg, a := range cfg.Auths {
			k[reg] = a
		}
	}
	return k, nil
}

var jsonUnmarshal = json.Unmarshal
