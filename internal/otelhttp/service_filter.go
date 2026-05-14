package otelhttp

import (
	"strings"

	collectorlogsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectortracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
)

func filterTracesByService(req *collectortracev1.ExportTraceServiceRequest, serviceName string) {
	if req == nil || !serviceFilterEnabled(serviceName) {
		return
	}
	filtered := req.ResourceSpans[:0]
	for _, resourceSpans := range req.GetResourceSpans() {
		if resourceServiceName(resourceSpans.GetResource().GetAttributes()) == serviceName {
			filtered = append(filtered, resourceSpans)
		}
	}
	req.ResourceSpans = filtered
}

func filterLogsByService(req *collectorlogsv1.ExportLogsServiceRequest, serviceName string) {
	if req == nil || !serviceFilterEnabled(serviceName) {
		return
	}
	filtered := req.ResourceLogs[:0]
	for _, resourceLogs := range req.GetResourceLogs() {
		if resourceServiceName(resourceLogs.GetResource().GetAttributes()) == serviceName {
			filtered = append(filtered, resourceLogs)
		}
	}
	req.ResourceLogs = filtered
}

func serviceFilterEnabled(serviceName string) bool {
	switch strings.ToLower(strings.TrimSpace(serviceName)) {
	case "", "all", "*", "none", "off", "false":
		return false
	default:
		return true
	}
}

func resourceServiceName(attrs []*commonv1.KeyValue) string {
	for _, attr := range attrs {
		if attr.GetKey() != "service.name" {
			continue
		}
		if value := attr.GetValue().GetStringValue(); value != "" {
			return value
		}
	}
	return ""
}
