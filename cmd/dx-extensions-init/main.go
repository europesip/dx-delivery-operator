/*
Copyright 2026 EUROPESIP.

Licensed under the MIT License (see LICENSE).
*/

// dx-extensions-init is the init container that the DX Delivery Operator injects into the DX Compose Web Engine
// pod (DXJavaExtension, issue #54). Before the Web Engine starts it pulls every Java extension from the registry
// by digest, verifies it, unpacks portlet WARs into resources/customApplications/<file>, writes shared-library JARs
// into libraries/customSharedLib and writes the Liberty <webApplication> configuration (configDropins/defaults).
// It exits non-zero, with a clear message, when a required extension cannot be loaded: the Web Engine does not
// start with half of its extensions.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"

	"github.com/europesip/dx-delivery-operator/internal/javaext"
)

type keychain map[string]authn.AuthConfig

func (k keychain) Resolve(r authn.Resource) (authn.Authenticator, error) {
	if a, ok := k[r.RegistryStr()]; ok {
		return authn.FromConfig(a), nil
	}
	return authn.Anonymous, nil
}

func loadAuth(secrets []string) (keychain, error) {
	k := keychain{}
	for _, s := range secrets {
		b, err := os.ReadFile(filepath.Join(javaext.InitPaths.Auth, s, ".dockerconfigjson"))
		if err != nil {
			return nil, fmt.Errorf("pull secret %s: %w", s, err)
		}
		var cfg struct {
			Auths map[string]authn.AuthConfig `json:"auths"`
		}
		if err := json.Unmarshal(b, &cfg); err != nil {
			return nil, fmt.Errorf("pull secret %s is not a dockerconfigjson: %w", s, err)
		}
		for reg, a := range cfg.Auths {
			k[reg] = a
		}
	}
	return k, nil
}

func logf(format string, a ...any) {
	fmt.Fprintf(os.Stdout, time.Now().UTC().Format("2006-01-02T15:04:05Z")+" "+format+"\n", a...)
}

func load(ctx context.Context, e javaext.Entry) error {
	kc, err := loadAuth(e.PullSecrets)
	if err != nil {
		return err
	}
	start := time.Now()
	f, err := javaext.Fetch(ctx, e.Ref, kc)
	if err != nil {
		return err
	}
	if f.Digest != e.Digest() {
		return fmt.Errorf("%s: got digest %s", e.Ref, f.Digest)
	}
	switch e.Type {
	case "sharedLibrary":
		names, err := javaext.WriteJARs(f.Data, e.File, javaext.InitPaths.Libs)
		if err != nil {
			return fmt.Errorf("%s: %w", e.Name, err)
		}
		logf("%s: %d JAR(s) %v from %s (%d bytes, %s)", e.Name, len(names), names, e.Ref, len(f.Data), time.Since(start).Round(time.Millisecond))
	default:
		dir := filepath.Join(javaext.InitPaths.Apps, e.File)
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
		n, err := javaext.UnpackWAR(f.Data, dir)
		if err != nil {
			return fmt.Errorf("%s: %w", e.Name, err)
		}
		if _, err := os.Stat(filepath.Join(dir, "WEB-INF", "web.xml")); err != nil {
			if _, err2 := os.Stat(filepath.Join(dir, "WEB-INF", "portlet.xml")); err2 != nil {
				return fmt.Errorf("%s: no WEB-INF/web.xml nor WEB-INF/portlet.xml: not a web application", e.Name)
			}
		}
		logf("%s: WAR %s with %d files, context root %s, from %s (%d bytes, %s)", e.Name, e.File, n, e.ContextRoot, e.Ref, len(f.Data), time.Since(start).Round(time.Millisecond))
	}
	return nil
}

func main() {
	var entries []javaext.Entry
	if err := json.Unmarshal([]byte(os.Getenv(javaext.EnvExtensions)), &entries); err != nil {
		logf("ERROR: %s is not a valid list: %v", javaext.EnvExtensions, err)
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	loaded := map[string]bool{}
	failed := 0
	for _, e := range entries {
		if err := load(ctx, e); err != nil {
			if e.Optional {
				logf("WARNING: optional extension %s not loaded, the Web Engine starts without it: %v", e.Name, err)
				continue
			}
			logf("ERROR: extension %s could not be loaded: %v", e.Name, err)
			failed++
			continue
		}
		loaded[e.Name] = true
	}
	if failed > 0 {
		logf("ERROR: %d required extension(s) missing: the Web Engine does not start. Fix the artifact (or its pull secret) and the pod is retried; the DXJavaExtension status says which one.", failed)
		os.Exit(1)
	}
	if err := os.WriteFile(filepath.Join(javaext.InitPaths.Config, javaext.LibertyFileName), []byte(javaext.LibertyXML(entries, loaded)), 0o644); err != nil {
		logf("ERROR: writing the Liberty configuration: %v", err)
		os.Exit(1)
	}
	logf("%d extension(s) ready for the Web Engine", len(loaded))
}
