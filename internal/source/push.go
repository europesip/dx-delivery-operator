package source

import (
	"context"
	"fmt"
	"sort"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"

	dxv1 "github.com/europesip/hcl-dx-scriptapp-operator/api/v1alpha1"
)

// ArtifactType of the artifacts the operator pushes (ORAS shows it; readers only need the file layers).
const ArtifactType = "application/vnd.europesip.dx.files.v1"

// FileArtifact builds an OCI artifact with one layer per file, named by org.opencontainers.image.title, the way
// "oras push <ref> file..." does: "oras pull" gets the files back, and the operator reads it as a source.
func FileArtifact(files map[string][]byte, mediaType string, annotations map[string]string) (v1.Image, error) {
	img := mutate.MediaType(empty.Image, types.OCIManifestSchema1)
	img = mutate.ConfigMediaType(img, types.MediaType(ArtifactType))
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	adds := make([]mutate.Addendum, 0, len(names))
	for _, n := range names {
		adds = append(adds, mutate.Addendum{
			Layer:       static.NewLayer(files[n], types.MediaType(mediaType)),
			Annotations: map[string]string{titleAnnotation: n},
			MediaType:   types.MediaType(mediaType),
		})
	}
	img, err := mutate.Append(img, adds...)
	if err != nil {
		return nil, err
	}
	if len(annotations) > 0 {
		img = mutate.Annotations(img, annotations).(v1.Image)
	}
	return img, nil
}

// Push writes files as an OCI artifact to ref (repository:tag), with the credentials of the dockerconfigjson Secrets,
// and returns repository@digest.
func (f *Fetcher) Push(ctx context.Context, namespace, ref string, secrets []dxv1.LocalObjectReference, files map[string][]byte, mediaType string, annotations map[string]string) (string, error) {
	r, err := name.ParseReference(ref)
	if err != nil {
		return "", fmt.Errorf("reference %q: %w", ref, err)
	}
	kc, err := f.keychain(ctx, namespace, secrets)
	if err != nil {
		return "", err
	}
	img, err := FileArtifact(files, mediaType, annotations)
	if err != nil {
		return "", err
	}
	if err := remote.Write(r, img, remote.WithContext(ctx), remote.WithAuthFromKeychain(kc)); err != nil {
		return "", fmt.Errorf("push %s: %w", ref, err)
	}
	d, err := img.Digest()
	if err != nil {
		return "", err
	}
	return r.Context().String() + "@" + d.String(), nil
}
