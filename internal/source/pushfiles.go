package source

// Pushing big local files as an OCI artifact in the layout of "oras push <ref> file...": an OCI image manifest whose config is
// the empty descriptor ({}) and whose layers are the files themselves, each named by its
// org.opencontainers.image.title annotation. No artifactType is set: the artifact is told apart by its repository
// (for example <project>/wcm/...), and fromImage reads it back as files.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/partial"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"

	dxv1 "github.com/europesip/hcl-dx-scriptapp-operator/api/v1alpha1"
)

// ArtifactFile is a local file to push, with the media type of its layer.
type ArtifactFile struct {
	Path      string // local file
	Title     string // file name in the artifact (default: the base name of Path)
	MediaType string // default application/octet-stream
}

// fileLayer is a layer whose blob is a local file as it is (never compressed again).
type fileLayer struct {
	path      string
	mediaType types.MediaType
	digest    v1.Hash
	size      int64
}

func newFileLayer(p, mediaType string) (*fileLayer, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return nil, err
	}
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	return &fileLayer{path: p, mediaType: types.MediaType(mediaType), digest: v1.Hash{Algorithm: "sha256", Hex: hex.EncodeToString(h.Sum(nil))}, size: n}, nil
}

func (l *fileLayer) Digest() (v1.Hash, error)             { return l.digest, nil }
func (l *fileLayer) DiffID() (v1.Hash, error)             { return l.digest, nil }
func (l *fileLayer) Compressed() (io.ReadCloser, error)   { return os.Open(l.path) }
func (l *fileLayer) Uncompressed() (io.ReadCloser, error) { return os.Open(l.path) }
func (l *fileLayer) Size() (int64, error)                 { return l.size, nil }
func (l *fileLayer) MediaType() (types.MediaType, error)  { return l.mediaType, nil }

// EmptyConfigMediaType is the media type of the empty config of an artifact (OCI image spec 1.1).
const EmptyConfigMediaType = "application/vnd.oci.empty.v1+json"

// fileArtifact is the image of an artifact: the empty config and one layer per file.
type fileArtifact struct {
	layers   []*fileLayer
	titles   []string
	manifest []byte
}

func (a *fileArtifact) MediaType() (types.MediaType, error) { return types.OCIManifestSchema1, nil }
func (a *fileArtifact) RawConfigFile() ([]byte, error)      { return []byte("{}"), nil }
func (a *fileArtifact) RawManifest() ([]byte, error)        { return a.manifest, nil }
func (a *fileArtifact) LayerByDigest(h v1.Hash) (partial.CompressedLayer, error) {
	for _, l := range a.layers {
		if l.digest == h {
			return l, nil
		}
	}
	return nil, fmt.Errorf("no layer %s", h)
}

func buildArtifact(files []ArtifactFile, annotations map[string]string) (v1.Image, error) {
	a := &fileArtifact{}
	cfg := []byte("{}")
	ch := sha256.Sum256(cfg)
	m := v1.Manifest{
		SchemaVersion: 2,
		MediaType:     types.OCIManifestSchema1,
		Config:        v1.Descriptor{MediaType: EmptyConfigMediaType, Size: int64(len(cfg)), Digest: v1.Hash{Algorithm: "sha256", Hex: hex.EncodeToString(ch[:])}, Data: cfg},
		Annotations:   annotations,
	}
	for _, f := range files {
		l, err := newFileLayer(f.Path, f.MediaType)
		if err != nil {
			return nil, err
		}
		title := f.Title
		if title == "" {
			title = filepath.Base(f.Path)
		}
		a.layers, a.titles = append(a.layers, l), append(a.titles, title)
		m.Layers = append(m.Layers, v1.Descriptor{MediaType: l.mediaType, Size: l.size, Digest: l.digest, Annotations: map[string]string{titleAnnotation: title}})
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	a.manifest = raw
	return partial.CompressedToImage(a)
}

// PushFiles pushes files as an artifact to ref with the credentials of the dockerconfigjson Secrets (namespace).
// It returns the digest of the manifest.
func (f *Fetcher) PushFiles(ctx context.Context, namespace, ref string, secrets []dxv1.LocalObjectReference, files []ArtifactFile, annotations map[string]string) (string, error) {
	r, err := name.ParseReference(ref)
	if err != nil {
		return "", fmt.Errorf("image reference %q: %w", ref, err)
	}
	kc, err := f.keychain(ctx, namespace, secrets)
	if err != nil {
		return "", err
	}
	img, err := buildArtifact(files, annotations)
	if err != nil {
		return "", err
	}
	if err := remote.Write(r, img, remote.WithContext(ctx), remote.WithAuthFromKeychain(kc)); err != nil {
		return "", fmt.Errorf("push to %s: %w", ref, err)
	}
	d, err := img.Digest()
	if err != nil {
		return "", err
	}
	return d.String(), nil
}
