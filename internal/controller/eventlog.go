// SPDX-License-Identifier: MIT
// Copyright (c) 2026 EUROPESIP

package controller

import (
	"fmt"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/recorder"
)

// loggingRecorder writes every Kubernetes event of the operator also as one structured log line (logger "events",
// key "dxopEvent": true), so that log stacks (Loki, Elastic…) get what the operator does even when the cluster does not
// collect events: pushes, drift, restores, chunk imports, failures.
type loggingRecorder struct {
	next recorder.EventRecorder
	log  logr.Logger
}

// LoggingRecorder wraps an event recorder so that each event is logged as well.
func LoggingRecorder(next recorder.EventRecorder, log logr.Logger) recorder.EventRecorder {
	return &loggingRecorder{next: next, log: log}
}

func (r *loggingRecorder) logEvent(regarding runtime.Object, eventtype, reason, action, note string, args ...interface{}) {
	kind, ns, name := "", "", ""
	if regarding != nil {
		kind = regarding.GetObjectKind().GroupVersionKind().Kind
		if acc, err := meta.Accessor(regarding); err == nil {
			ns, name = acc.GetNamespace(), acc.GetName()
		}
		if kind == "" {
			kind = fmt.Sprintf("%T", regarding)
			if i := len(kind) - 1; i > 0 {
				for j := i; j >= 0; j-- {
					if kind[j] == '.' {
						kind = kind[j+1:]
						break
					}
				}
			}
		}
	}
	r.log.Info(fmt.Sprintf(note, args...), "dxopEvent", true, "eventType", eventtype, "reason", reason, "action", action,
		"kind", kind, "resourceNamespace", ns, "resourceName", name)
}

func (r *loggingRecorder) Eventf(regarding runtime.Object, related runtime.Object, eventtype, reason, action, note string, args ...interface{}) {
	r.logEvent(regarding, eventtype, reason, action, note, args...)
	r.next.Eventf(regarding, related, eventtype, reason, action, note, args...)
}

func (r *loggingRecorder) AnnotatedEventf(regarding runtime.Object, related runtime.Object, annotations map[string]string, eventtype, reason, action, note string, args ...interface{}) {
	r.logEvent(regarding, eventtype, reason, action, note, args...)
	r.next.AnnotatedEventf(regarding, related, annotations, eventtype, reason, action, note, args...)
}
