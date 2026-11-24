// SPDX-License-Identifier: MIT

/*
Copyright 2026 EUROPESIP.

Licensed under the MIT License (see LICENSE).
*/

package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dxv1 "github.com/europesip/dx-delivery-operator/api/v1alpha1"
	"github.com/europesip/dx-delivery-operator/internal/javaext"
)

// SeedEnv: the files the seed job writes (JSON list of javaext.SeedFile) and the folder (the startup volume).
const (
	SeedEnv      = "DX_JAVA_EXTENSIONS_SEED"
	seedMountDir = "/seed"
)

// deliveryConflict says why this extension cannot be delivered ("" = no conflict): one Web Engine uses one mode
// (issue #72). The webhook label on the namespace makes the webhook act on every Web Engine pod there, so the
// startup hook mode needs a namespace without it; and all the extensions of one StatefulSet use the same mode.
func (r *DXJavaExtensionReconciler) deliveryConflict(ctx context.Context, e *dxv1.DXJavaExtension, ns *corev1.Namespace) (string, error) {
	mode := e.DeliveryMode()
	if mode == dxv1.DeliveryStartupHook && ns.Labels[dxv1.LabelJavaExtensionsNamespace] == "enabled" {
		return fmt.Sprintf("delivery startupHook, but the namespace has the label %s=enabled (webhook delivery): the two modes cannot be used together on one portal; remove the label or use delivery webhook", dxv1.LabelJavaExtensionsNamespace), nil
	}
	var list dxv1.DXJavaExtensionList
	if err := r.APIReader.List(ctx, &list, client.InNamespace(e.Namespace)); err != nil {
		return "", err
	}
	var other []string
	for i := range list.Items {
		o := &list.Items[i]
		if o.Name == e.Name || o.StatefulSetName() != e.StatefulSetName() || !o.DeletionTimestamp.IsZero() {
			continue
		}
		if o.DeliveryMode() != mode {
			other = append(other, o.Name+" ("+o.DeliveryMode()+")")
		}
	}
	if len(other) > 0 {
		return fmt.Sprintf("delivery %s, but other extensions of the Web Engine %s use another mode: %s; all the extensions of one Web Engine must use the same delivery", mode, e.StatefulSetName(), strings.Join(other, ", ")), nil
	}
	return "", nil
}

// ensureStartupHook makes sure the startup volume of the Web Engine holds the current script and token (issue
// #72). It writes them with a short job the first time (and when they change), then records the hash on the PVC.
func (r *DXJavaExtensionReconciler) ensureStartupHook(ctx context.Context, e *dxv1.DXJavaExtension) (bool, ctrl.Result, error) {
	if r.StartupHookURL == "" || len(r.StartupHookKey) == 0 || r.SeedImage == "" {
		r.fail(e, dxv1.ConditionReady, reasonJXHookOff, "delivery startupHook is not enabled in this operator (chart value javaExtensions.startupHook.enabled, and javaExtensions.enabled)")
		return false, ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
	}
	var sts appsv1.StatefulSet
	if err := r.Get(ctx, client.ObjectKey{Namespace: e.Namespace, Name: e.StatefulSetName()}, &sts); err != nil {
		if apierrors.IsNotFound(err) {
			r.fail(e, dxv1.ConditionReady, reasonJXNoVolume, "StatefulSet "+e.StatefulSetName()+" does not exist")
			return false, ctrl.Result{RequeueAfter: time.Minute}, nil
		}
		return false, ctrl.Result{}, err
	}
	claim := javaext.StartupVolume(&sts, e.ContainerName(), dxv1.StartupHookDir)
	if claim == "" {
		r.fail(e, dxv1.ConditionReady, reasonJXNoVolume, fmt.Sprintf("delivery startupHook needs a volume on %s in the Web Engine: add to the HCL chart values volumes.webEngine.customPVCs an entry with mountPath %s (ReadWriteMany storage for more than one replica) and helm upgrade the portal", dxv1.StartupHookDir, dxv1.StartupHookDir))
		return false, ctrl.Result{RequeueAfter: 2 * time.Minute}, nil
	}
	e.Status.StartupHookClaim = claim
	script := javaext.StartupScript(r.StartupHookURL, e.Namespace, e.StatefulSetName())
	token := javaext.StartupToken(r.StartupHookKey, e.Namespace, e.StatefulSetName())
	want := hashOf(script, token)
	var pvc corev1.PersistentVolumeClaim
	if err := r.Get(ctx, client.ObjectKey{Namespace: e.Namespace, Name: claim}, &pvc); err != nil {
		if apierrors.IsNotFound(err) {
			r.fail(e, dxv1.ConditionReady, reasonJXNoVolume, "the startup volume (PVC "+claim+") does not exist yet")
			return false, ctrl.Result{RequeueAfter: time.Minute}, nil
		}
		return false, ctrl.Result{}, err
	}
	if pvc.Annotations[dxv1.AnnotationStartupHookSeeded] == want {
		return true, ctrl.Result{}, nil
	}
	jobName := seedJobName(e.StatefulSetName(), want)
	var job batchv1.Job
	err := r.Get(ctx, client.ObjectKey{Namespace: e.Namespace, Name: jobName}, &job)
	switch {
	case apierrors.IsNotFound(err):
		j, err := r.seedJob(ctx, e, &sts, claim, jobName, script, token)
		if err != nil {
			return false, ctrl.Result{}, err
		}
		if err := r.Create(ctx, j); err != nil && !apierrors.IsAlreadyExists(err) {
			return false, ctrl.Result{}, err
		}
		r.Recorder.Eventf(e, nil, corev1.EventTypeNormal, "SeedingStartupVolume", "Seed", "writing the startup script on the volume %s (job %s)", claim, jobName)
	case err != nil:
		return false, ctrl.Result{}, err
	case job.Status.Succeeded > 0:
		patch, _ := json.Marshal(map[string]any{"metadata": map[string]any{"annotations": map[string]any{dxv1.AnnotationStartupHookSeeded: want}}})
		if err := r.Patch(ctx, &pvc, client.RawPatch(types.MergePatchType, patch)); err != nil {
			return false, ctrl.Result{}, err
		}
		_ = r.Delete(ctx, &job, client.PropagationPolicy(metav1.DeletePropagationBackground))
		r.Recorder.Eventf(e, nil, corev1.EventTypeNormal, "StartupVolumeSeeded", "Seed", "startup script written on the volume %s", claim)
		return true, ctrl.Result{}, nil
	case job.Status.Failed > 0 && jobFailed(&job):
		r.fail(e, dxv1.ConditionReady, reasonJXSeedFailed, fmt.Sprintf("the job %s could not write the startup script on the volume %s (see its log); delete the job to try again", jobName, claim))
		return false, ctrl.Result{RequeueAfter: 2 * time.Minute}, nil
	}
	r.fail(e, dxv1.ConditionReady, reasonJXSeeding, "writing the startup script on the volume "+claim+" (job "+jobName+")")
	return false, ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

func jobFailed(j *batchv1.Job) bool {
	for _, c := range j.Status.Conditions {
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// seedJobName: dx-jx-seed-<statefulset>-<hash>, at most 63 characters.
func seedJobName(sts, hash string) string {
	h := strings.TrimPrefix(hash, "sha256:")
	if len(h) > 8 {
		h = h[:8]
	}
	base := "dx-jx-seed-" + sts
	if len(base) > 63-9 {
		base = base[:63-9]
	}
	return strings.TrimSuffix(base, "-") + "-" + h
}

// seedJob writes the script and the token on the startup volume with the init image ("seed" command), as the DX
// user of the Web Engine (the files must be readable and the script executable by it). On a node where a Web Engine
// pod already runs when the volume is ReadWriteOnce.
func (r *DXJavaExtensionReconciler) seedJob(ctx context.Context, e *dxv1.DXJavaExtension, sts *appsv1.StatefulSet, claim, name, script, token string) (*batchv1.Job, error) {
	files, _ := json.Marshal([]javaext.SeedFile{
		{Name: javaext.StartupScriptName, Content: script, Mode: 0o750},
		{Name: javaext.StartupTokenName, Content: token, Mode: 0o640},
	})
	uid, gid, fsGroup := javaext.PodSecurityOf(&sts.Spec.Template.Spec, e.ContainerName())
	f, nonRoot := false, true
	backoff, ttl := int32(2), int32(3600)
	pod := corev1.PodSpec{
		RestartPolicy: corev1.RestartPolicyNever,
		SecurityContext: &corev1.PodSecurityContext{RunAsUser: uid, RunAsGroup: gid, FSGroup: fsGroup, RunAsNonRoot: &nonRoot,
			SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}},
		Containers: []corev1.Container{{
			Name: "seed", Image: r.SeedImage, ImagePullPolicy: r.SeedPullPolicy,
			Args: []string{"seed"},
			Env:  []corev1.EnvVar{{Name: SeedEnv, Value: string(files)}, {Name: "DX_JAVA_EXTENSIONS_SEED_DIR", Value: seedMountDir}},
			SecurityContext: &corev1.SecurityContext{AllowPrivilegeEscalation: &f,
				Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}},
			VolumeMounts: []corev1.VolumeMount{{Name: "startup", MountPath: seedMountDir}},
		}},
		Volumes: []corev1.Volume{{Name: "startup", VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: claim}}}},
	}
	if r.SeedPullSecret != "" {
		pod.ImagePullSecrets = []corev1.LocalObjectReference{{Name: r.SeedPullSecret}}
	}
	// A ReadWriteOnce volume already attached to a running Web Engine pod can only be mounted on that node.
	var pods corev1.PodList
	if err := r.List(ctx, &pods, client.InNamespace(e.Namespace)); err != nil {
		return nil, err
	}
	for i := range pods.Items {
		p := &pods.Items[i]
		if ownedBy(p, sts.Name) && p.Spec.NodeName != "" && p.DeletionTimestamp.IsZero() {
			pod.NodeName = p.Spec.NodeName
			break
		}
	}
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Namespace: e.Namespace, Name: name,
			Labels: map[string]string{"app.kubernetes.io/managed-by": "dx-delivery-operator", "app.kubernetes.io/component": "java-extensions-seed"}},
		Spec: batchv1.JobSpec{BackoffLimit: &backoff, TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app.kubernetes.io/component": "java-extensions-seed"}}, Spec: pod}},
	}, nil
}
