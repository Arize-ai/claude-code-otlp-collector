package otelhttp

import (
	"testing"

	collectorlogsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectortracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
)

func TestAddProjectAttributesFromResourceToTraces(t *testing.T) {
	req := &collectortracev1.ExportTraceServiceRequest{
		ResourceSpans: []*tracev1.ResourceSpans{
			{
				Resource: resourceWithAttrs(map[string]string{
					"team.name":                  "search",
					"openinference.project.name": "fallback-project",
				}),
			},
		},
	}

	addProjectAttributesFromResourceToTraces(req, "team.name")

	attrs := req.GetResourceSpans()[0].GetResource().GetAttributes()
	for _, key := range []string{"openinference.project.name", "arize.project.name", "project.name", "model_id"} {
		if got, _ := resourceString(attrs, key); got != "search" {
			t.Fatalf("%s = %q, want search", key, got)
		}
	}
	if got, _ := resourceString(attrs, "team.name"); got != "search" {
		t.Fatalf("team.name = %q, want search", got)
	}
}

func TestAddProjectAttributesFromResourceToTracesMissingAttrKeepsFallback(t *testing.T) {
	req := &collectortracev1.ExportTraceServiceRequest{
		ResourceSpans: []*tracev1.ResourceSpans{
			{
				Resource: resourceWithAttrs(map[string]string{
					"openinference.project.name": "fallback-project",
				}),
			},
		},
	}

	addProjectAttributesFromResourceToTraces(req, "team.name")

	if got, _ := resourceString(req.GetResourceSpans()[0].GetResource().GetAttributes(), "openinference.project.name"); got != "fallback-project" {
		t.Fatalf("openinference.project.name = %q, want fallback-project", got)
	}
}

func TestAddProjectAttributesFromResourceToLogs(t *testing.T) {
	req := &collectorlogsv1.ExportLogsServiceRequest{
		ResourceLogs: []*logsv1.ResourceLogs{
			{
				Resource: resourceWithAttrs(map[string]string{
					"team.name": "infra",
				}),
			},
		},
	}

	addProjectAttributesFromResourceToLogs(req, "team.name")

	attrs := req.GetResourceLogs()[0].GetResource().GetAttributes()
	for _, key := range []string{"openinference.project.name", "arize.project.name", "project.name", "model_id"} {
		if got, _ := resourceString(attrs, key); got != "infra" {
			t.Fatalf("%s = %q, want infra", key, got)
		}
	}
}

func resourceWithAttrs(attrs map[string]string) *resourcev1.Resource {
	resource := &resourcev1.Resource{}
	for key, value := range attrs {
		resource.Attributes = append(resource.Attributes, &commonv1.KeyValue{
			Key:   key,
			Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: value}},
		})
	}
	return resource
}
