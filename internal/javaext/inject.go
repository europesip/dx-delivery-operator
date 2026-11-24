/*
Copyright 2026 EUROPESIP.

Licensed under the MIT License (see LICENSE).
*/

package javaext

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// Paths of the DX Compose Web Engine (Open Liberty) where the extensions go.
const (
	LibertyServer   = "/opt/openliberty/wlp/usr/servers/defaultServer"
	AppsDir         = LibertyServer + "/resources/customApplications" // HCL: custom WARs since CF230
	LibsDir         = LibertyServer + "/libraries/customSharedLib"    // HCL: global classpath JARs since CF236
	ConfigDefaults  = LibertyServer + "/configDropins/defaults"       // Liberty reads it; empty in the HCL image
	LibertyFileName = "dx-java-extensions.xml"
)

// Names of what the webhook adds to the pod.
const (
	InitContainerName = "dx-java-extensions"
	VolumeApps        = "dx-jx-apps"
	VolumeLibs        = "dx-jx-libs"
	VolumeConfig      = "dx-jx-config"
	volumeAuthPrefix  = "dx-jx-auth-"
	initApps          = "/out/apps"
	initLibs          = "/out/libs"
	initConfig        = "/out/config"
	initAuth          = "/auth"
	// EnvExtensions: the JSON list of Entry for the init container.
	EnvExtensions = "DX_JAVA_EXTENSIONS"
)

// Entry is one extension for the init container (and what the webhook injects).
type Entry struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Ref         string `json:"ref"` // repository@digest
	File        string `json:"file"`
	ContextRoot string `json:"contextRoot,omitempty"`
	Optional    bool   `json:"optional,omitempty"`
	// PullSecrets: names of Secrets (dockerconfigjson) mounted under /auth/<name>/.dockerconfigjson.
	PullSecrets []string `json:"pullSecrets,omitempty"`
}

// Digest of the entry (the part after @).
func (e Entry) Digest() string {
	if i := strings.LastIndex(e.Ref, "@"); i >= 0 {
		return e.Ref[i+1:]
	}
	return ""
}

// SortEntries orders entries by name (stable patches and hashes).
func SortEntries(es []Entry) { sort.Slice(es, func(i, j int) bool { return es[i].Name < es[j].Name }) }

// Summary is name@digest of every entry (the pod annotation and the rollout hash input).
func Summary(es []Entry) string {
	parts := make([]string, 0, len(es))
	for _, e := range es {
		parts = append(parts, e.Name+"@"+e.Digest())
	}
	return strings.Join(parts, ",")
}

func xmlAttr(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

// LibertyXML is the Liberty configuration that starts every portlet WAR (configDropins/defaults).
func LibertyXML(es []Entry, loaded map[string]bool) string {
	var b strings.Builder
	b.WriteString("<server description=\"DX Java extensions (DX Delivery Operator, DXJavaExtension)\">\n")
	for _, e := range es {
		if e.Type != "portlet" || (loaded != nil && !loaded[e.Name]) {
			continue
		}
		ctx := strings.TrimPrefix(e.ContextRoot, "/")
		fmt.Fprintf(&b, "  <webApplication id=\"%s\" name=\"%s\" contextRoot=\"%s\" location=\"${server.config.dir}/resources/customApplications/%s\"/>\n",
			xmlAttr(e.File), xmlAttr(e.File), xmlAttr(ctx), xmlAttr(e.File))
	}
	b.WriteString("</server>\n")
	return b.String()
}

// InjectOptions: how the webhook changes the Web Engine pod.
type InjectOptions struct {
	// Container: the Web Engine container.
	Container string
	// InitImage: the image of the init container (dx-extensions-init).
	InitImage string
	// PullPolicy of the init image.
	PullPolicy corev1.PullPolicy
	// ImagePullSecret: a Secret in the portal namespace to pull the init image from a private registry (added to
	// the pod when it is not there yet).
	ImagePullSecret string
}

// Inject adds the init container, the volumes and the mounts to a Web Engine pod for these entries. It returns
// false when there is nothing to do (no entries, no such container, or already injected).
func Inject(pod *corev1.Pod, es []Entry, o InjectOptions) (bool, error) {
	if len(es) == 0 {
		return false, nil
	}
	for _, c := range pod.Spec.InitContainers {
		if c.Name == InitContainerName {
			return false, nil
		}
	}
	ci := -1
	for i, c := range pod.Spec.Containers {
		if c.Name == o.Container {
			ci = i
		}
	}
	if ci < 0 {
		return false, nil
	}
	SortEntries(es)
	hasLibs := false
	secrets := map[string]bool{}
	for _, e := range es {
		if e.Type == "sharedLibrary" {
			hasLibs = true
		}
		for _, s := range e.PullSecrets {
			secrets[s] = true
		}
	}
	payload, err := json.Marshal(es)
	if err != nil {
		return false, err
	}
	empty := func(n string) corev1.Volume {
		return corev1.Volume{Name: n, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}
	}
	pod.Spec.Volumes = append(pod.Spec.Volumes, empty(VolumeApps), empty(VolumeConfig))
	init := corev1.Container{
		Name:            InitContainerName,
		Image:           o.InitImage,
		ImagePullPolicy: o.PullPolicy,
		Env:             []corev1.EnvVar{{Name: EnvExtensions, Value: string(payload)}},
		VolumeMounts: []corev1.VolumeMount{
			{Name: VolumeApps, MountPath: initApps},
			{Name: VolumeConfig, MountPath: initConfig},
		},
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: ptrBool(false),
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		},
		Resources: corev1.ResourceRequirements{},
	}
	mounts := []corev1.VolumeMount{
		{Name: VolumeApps, MountPath: AppsDir},
		{Name: VolumeConfig, MountPath: ConfigDefaults, ReadOnly: true},
	}
	if hasLibs {
		pod.Spec.Volumes = append(pod.Spec.Volumes, empty(VolumeLibs))
		init.VolumeMounts = append(init.VolumeMounts, corev1.VolumeMount{Name: VolumeLibs, MountPath: initLibs})
		mounts = append(mounts, corev1.VolumeMount{Name: VolumeLibs, MountPath: LibsDir})
	}
	names := make([]string, 0, len(secrets))
	for s := range secrets {
		names = append(names, s)
	}
	sort.Strings(names)
	for i, s := range names {
		vn := fmt.Sprintf("%s%d", volumeAuthPrefix, i)
		pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{Name: vn, VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: s}}})
		init.VolumeMounts = append(init.VolumeMounts, corev1.VolumeMount{Name: vn, MountPath: initAuth + "/" + s, ReadOnly: true})
	}
	sameUserAsWebEngine(&init, pod, pod.Spec.Containers[ci])
	pod.Spec.InitContainers = append(pod.Spec.InitContainers, init)
	if o.ImagePullSecret != "" {
		has := false
		for _, r := range pod.Spec.ImagePullSecrets {
			has = has || r.Name == o.ImagePullSecret
		}
		if !has {
			pod.Spec.ImagePullSecrets = append(pod.Spec.ImagePullSecrets, corev1.LocalObjectReference{Name: o.ImagePullSecret})
		}
	}
	pod.Spec.Containers[ci].VolumeMounts = append(pod.Spec.Containers[ci].VolumeMounts, mounts...)
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	pod.Annotations["dx.europesip.com/java-extensions"] = Summary(es)
	return true, nil
}

func ptrBool(b bool) *bool { return &b }

// sameUserAsWebEngine (issue #63): the init container runs as the same user as DX. The HCL chart sets the UID and GID
// at pod level (1000/1001, inside the UID range of the OpenShift namespace annotations HCL asks for), and the init
// container inherits them. When the pod has none but the Web Engine container has its own, the init container takes
// them; otherwise it would run as the UID of its image (65532, distroless nonroot), outside that range on OpenShift.
// With neither, nothing is set: the platform decides (the SCC of OpenShift gives the namespace UID to the whole pod).
func sameUserAsWebEngine(init *corev1.Container, pod *corev1.Pod, we corev1.Container) {
	psc := pod.Spec.SecurityContext
	csc := we.SecurityContext
	if csc == nil {
		return
	}
	if (psc == nil || psc.RunAsUser == nil) && csc.RunAsUser != nil {
		u := *csc.RunAsUser
		init.SecurityContext.RunAsUser = &u
	}
	if (psc == nil || psc.RunAsGroup == nil) && csc.RunAsGroup != nil {
		g := *csc.RunAsGroup
		init.SecurityContext.RunAsGroup = &g
	}
	if csc.RunAsNonRoot != nil && (psc == nil || psc.RunAsNonRoot == nil) {
		n := *csc.RunAsNonRoot
		init.SecurityContext.RunAsNonRoot = &n
	}
}

// InitPaths are the folders of the init container (for the init binary).
var InitPaths = struct{ Apps, Libs, Config, Auth string }{initApps, initLibs, initConfig, initAuth}
