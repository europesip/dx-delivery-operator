// SPDX-License-Identifier: MIT

/*
Copyright 2026 EUROPESIP.

Licensed under the MIT License (see LICENSE).
*/

package javaext

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

// Delivery through the HCL customer startup folder (issue #72). The entrypoint of the DX Compose Web Engine image
// runs every *.sh of svrcfg/bin/customer/startup on each container start, after its own configuration and before
// the final Liberty start (CF239, scripts/utility.sh execute_custom_scripts; exit code 2 aborts the start). A
// volume of the HCL chart (volumes.webEngine.customPVCs) mounted there holds a small generic script written once
// by the operator; on every start the script downloads the extensions of its Web Engine through the operator
// (no registry credentials in the DX container) and writes them where the init container of the webhook mode
// writes them.

const (
	// StartupScriptName and StartupTokenName: the files the operator writes on the startup volume.
	StartupScriptName = "dx-java-extensions.sh"
	StartupTokenName  = "dx-java-extensions.token"
	// StartupHookPath: the endpoint of the operator that serves the bundle of a Web Engine.
	StartupHookPath = "/dx-java-extensions/v1/bundle"
	// BundleSummaryFile: name@digest of every extension in a bundle (the pod annotation).
	BundleSummaryFile = "SUMMARY"
)

// StartupToken is the per-Web-Engine token of the startup script: HMAC-SHA256 of namespace/statefulset with the
// key of the operator. The endpoint accepts it only for that StatefulSet.
func StartupToken(key []byte, namespace, statefulSet string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(namespace + "/" + statefulSet))
	return hex.EncodeToString(m.Sum(nil))
}

// ValidStartupToken compares in constant time.
func ValidStartupToken(key []byte, namespace, statefulSet, token string) bool {
	return len(key) > 0 && hmac.Equal([]byte(StartupToken(key, namespace, statefulSet)), []byte(token))
}

// StartupVolume returns the PVC mounted on the HCL customer startup folder of the Web Engine container ("" when
// there is none).
func StartupVolume(sts *appsv1.StatefulSet, container, dir string) string {
	vol := ""
	for _, c := range sts.Spec.Template.Spec.Containers {
		if c.Name != container {
			continue
		}
		for _, m := range c.VolumeMounts {
			if filepath.Clean(m.MountPath) == filepath.Clean(dir) && m.SubPath == "" {
				vol = m.Name
			}
		}
	}
	if vol == "" {
		return ""
	}
	for _, v := range sts.Spec.Template.Spec.Volumes {
		if v.Name == vol && v.PersistentVolumeClaim != nil {
			return v.PersistentVolumeClaim.ClaimName
		}
	}
	return ""
}

// PodSecurityOf returns the UID/GID the Web Engine container runs with (pod level, or the container), for the
// job that writes the startup volume: the files must belong to the DX user (fsGroup makes the volume writable).
func PodSecurityOf(spec *corev1.PodSpec, container string) (uid, gid, fsGroup *int64) {
	if psc := spec.SecurityContext; psc != nil {
		uid, gid, fsGroup = psc.RunAsUser, psc.RunAsGroup, psc.FSGroup
	}
	for _, c := range spec.Containers {
		if c.Name == container && c.SecurityContext != nil {
			if c.SecurityContext.RunAsUser != nil {
				uid = c.SecurityContext.RunAsUser
			}
			if c.SecurityContext.RunAsGroup != nil {
				gid = c.SecurityContext.RunAsGroup
			}
		}
	}
	return uid, gid, fsGroup
}

// StartupScript is the script the operator writes on the startup volume. It is generic: the URL of the operator,
// the namespace and the StatefulSet are the only values; what to load is asked on every start.
func StartupScript(operatorURL, namespace, statefulSet string) string {
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
	return `#!/bin/bash
# SPDX-License-Identifier: MIT
# Copyright (c) 2026 EUROPESIP
# DX Delivery Operator, by EUROPESIP: DXJavaExtension, delivery "startupHook" (issue #72).
# Written by the operator; do not edit (it is written again when it changes).
# The HCL Web Engine entrypoint runs this script on every container start (svrcfg/bin/customer/startup), before the
# final Liberty start. It downloads the Java extensions of this Web Engine through the operator (the registry
# credentials never reach this container) and writes them into resources/customApplications,
# libraries/customSharedLib and configDropins/defaults. Exit code 2 (abort_container) stops the start.
source /opt/openliberty/wlp/usr/svrcfg/scripts/utility.sh 2>/dev/null
log() { if type log_message_customer >/dev/null 2>&1; then log_message_customer "[dx-java-extensions] $*"; else echo "[dx-java-extensions] $*"; fi; }
fail() {
  if type abort_container >/dev/null 2>&1; then abort_container "[dx-java-extensions] $*"; fi
  log "CRITICAL: $*"; exit 2
}
OPERATOR_URL=` + q(operatorURL) + `
NAMESPACE=` + q(namespace) + `
STATEFULSET=` + q(statefulSet) + `
HERE=$(cd "$(dirname "$0")" && pwd)
TOKEN=$(cat "$HERE/` + StartupTokenName + `" 2>/dev/null) || fail "token file missing in $HERE"
SERVER=/opt/openliberty/wlp/usr/servers/defaultServer
APPS=$SERVER/resources/customApplications
LIBS=$SERVER/libraries/customSharedLib
CONF=$SERVER/configDropins/defaults
STATE=$SERVER/dx-java-extensions.files
TMP=$(mktemp -d) || fail "mktemp"
trap 'rm -rf "$TMP"' EXIT
URL="$OPERATOR_URL` + StartupHookPath + `?namespace=$NAMESPACE&statefulset=$STATEFULSET&pod=${HOSTNAME}"
code=000
for i in 1 2 3 4 5 6 7 8 9 10 11 12; do
  code=$(curl -sS -o "$TMP/bundle" -w '%{http_code}' -H "Authorization: Bearer $TOKEN" --connect-timeout 10 --max-time 600 "$URL") && [ "$code" != 000 ] && break
  log "the operator does not answer (attempt $i of 12), retrying in 10 s"
  sleep 10
done
# What an earlier start of this container wrote goes away first (a version change or a removed extension).
if [ -f "$STATE" ]; then
  while IFS= read -r p; do case "$p" in "$APPS"/*|"$LIBS"/*|"$CONF"/*) rm -rf -- "$p" ;; esac; done < "$STATE"
  rm -f "$STATE"
fi
case "$code" in
  204) log "no Java extensions for $NAMESPACE/$STATEFULSET"; exit 0 ;;
  200) ;;
  *) fail "the operator answered HTTP $code: $(head -c 400 "$TMP/bundle" 2>/dev/null)" ;;
esac
mkdir -p "$TMP/x" "$APPS" "$LIBS" "$CONF" || fail "cannot create the target folders"
tar -xzmf "$TMP/bundle" -C "$TMP/x" || fail "cannot unpack the bundle"
: > "$STATE"
for d in "$TMP"/x/apps/*; do
  [ -e "$d" ] || continue
  n=$(basename "$d"); rm -rf -- "$APPS/$n"
  cp -R "$d" "$APPS/$n" || fail "cannot write $APPS/$n"
  echo "$APPS/$n" >> "$STATE"
done
for f in "$TMP"/x/libs/*; do
  [ -e "$f" ] || continue
  n=$(basename "$f"); cp "$f" "$LIBS/$n" || fail "cannot write $LIBS/$n"
  echo "$LIBS/$n" >> "$STATE"
done
for f in "$TMP"/x/config/*; do
  [ -e "$f" ] || continue
  n=$(basename "$f"); cp "$f" "$CONF/$n" || fail "cannot write $CONF/$n"
  echo "$CONF/$n" >> "$STATE"
done
log "Java extensions ready: $(cat "$TMP/x/` + BundleSummaryFile + `" 2>/dev/null)"
exit 0
`
}

// BundleResult: what was put in a bundle.
type BundleResult struct {
	Data    []byte          // tar.gz: apps/<file>/…, libs/<jar>, config/<LibertyFileName>, SUMMARY
	Loaded  map[string]bool // extension name -> loaded
	Summary string          // name@digest of the loaded extensions
	Skipped []string        // optional extensions that could not be loaded (with the reason)
}

// BuildBundle pulls every extension by digest and builds the tar.gz the startup script unpacks. A required
// extension that cannot be loaded is an error (the start is aborted, like the init container); an optional one is
// skipped.
func BuildBundle(ctx context.Context, es []Entry, keychain func(Entry) (authn.Keychain, error)) (*BundleResult, error) {
	dir, err := os.MkdirTemp("", "dx-jx-bundle-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir) //nolint:errcheck
	apps, libs, cfg := filepath.Join(dir, "apps"), filepath.Join(dir, "libs"), filepath.Join(dir, "config")
	for _, d := range []string{apps, libs, cfg} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	res := &BundleResult{Loaded: map[string]bool{}}
	var loaded []Entry
	for _, e := range es {
		err := func() error {
			kc, err := keychain(e)
			if err != nil {
				return err
			}
			f, err := Fetch(ctx, e.Ref, kc)
			if err != nil {
				return err
			}
			if f.Digest != e.Digest() {
				return fmt.Errorf("%s: got digest %s", e.Ref, f.Digest)
			}
			if e.Type == "sharedLibrary" {
				_, err = WriteJARs(f.Data, e.File, libs)
				return err
			}
			target := filepath.Join(apps, e.File)
			if _, err := UnpackWAR(f.Data, target); err != nil {
				return err
			}
			if _, err := os.Stat(filepath.Join(target, "WEB-INF", "web.xml")); err != nil {
				if _, err2 := os.Stat(filepath.Join(target, "WEB-INF", "portlet.xml")); err2 != nil {
					return fmt.Errorf("no WEB-INF/web.xml nor WEB-INF/portlet.xml: not a web application")
				}
			}
			return nil
		}()
		if err != nil {
			if e.Optional {
				res.Skipped = append(res.Skipped, e.Name+": "+err.Error())
				continue
			}
			return nil, fmt.Errorf("extension %s: %w", e.Name, err)
		}
		res.Loaded[e.Name] = true
		loaded = append(loaded, e)
	}
	res.Summary = Summary(loaded)
	if err := os.WriteFile(filepath.Join(cfg, LibertyFileName), []byte(LibertyXML(es, res.Loaded)), 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, BundleSummaryFile), []byte(res.Summary+"\n"), 0o644); err != nil {
		return nil, err
	}
	if res.Data, err = tarGz(dir); err != nil {
		return nil, err
	}
	return res, nil
}

// tarGz packs a folder (relative paths, regular files and folders only, readable by any user).
func tarGz(root string) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if rel == "." {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return tw.WriteHeader(&tar.Header{Name: filepath.ToSlash(rel) + "/", Mode: 0o755, Typeflag: tar.TypeDir, ModTime: info.ModTime()})
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: filepath.ToSlash(rel), Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg, ModTime: info.ModTime()}); err != nil {
			return err
		}
		_, err = tw.Write(data)
		return err
	})
	if err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// SeedFile is a file the seed job writes on the startup volume.
type SeedFile struct {
	Name    string      `json:"name"`
	Content string      `json:"content"`
	Mode    os.FileMode `json:"mode"`
}

// WriteSeed writes the files into dir (the startup volume) atomically: a temporary file and a rename, so the Web
// Engine never runs half a script.
func WriteSeed(dir string, files []SeedFile) error {
	for _, f := range files {
		if f.Name == "" || strings.ContainsAny(f.Name, `/\`) || strings.HasPrefix(f.Name, ".") {
			return fmt.Errorf("invalid seed file name %q", f.Name)
		}
		tmp := filepath.Join(dir, "."+f.Name+".tmp")
		if err := os.WriteFile(tmp, []byte(f.Content), f.Mode); err != nil {
			return err
		}
		if err := os.Chmod(tmp, f.Mode); err != nil { // the umask must not change the mode
			return err
		}
		if err := os.Rename(tmp, filepath.Join(dir, f.Name)); err != nil {
			return err
		}
	}
	return nil
}
