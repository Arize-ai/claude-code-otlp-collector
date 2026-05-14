package otelhttp

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/arize-ai/claude-code-otlp-collector/internal/mapping"
	collectorlogsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectortracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type Server struct {
	ListenAddress                string
	ExporterEndpoint             string
	ExporterHeaders              map[string]string
	ForwardLogs                  bool
	ResourceAttributes           map[string]string
	ProjectFromResourceAttribute string
	ServiceNameFilter            string
	TraceDelay                   time.Duration
	FileExporter                 *FileExporter
	OutputStore                  *mapping.OutputStore
	Logger                       *slog.Logger
}

func (s Server) Run(ctx context.Context) error {
	if s.Logger == nil {
		s.Logger = slog.Default()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.health)
	mux.HandleFunc("/v1/traces", s.traces)
	mux.HandleFunc("/v1/logs", s.logs)

	server := &http.Server{
		Addr:              s.ListenAddress,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		s.Logger.Info("collector listening", "listen", s.ListenAddress, "exporter_endpoint", s.ExporterEndpoint)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

func (s Server) health(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func (s Server) traces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	payload, err := readBody(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req := &collectortracev1.ExportTraceServiceRequest{}
	if err := unmarshalOTLP(r.Header.Get("Content-Type"), payload, req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	filterTracesByService(req, s.ServiceNameFilter)
	if len(req.GetResourceSpans()) == 0 {
		writeEmptyExportResponse(w, "/v1/traces")
		return
	}
	addResourceAttributesToTraces(req, s.ResourceAttributes)
	addProjectAttributesFromResourceToTraces(req, s.ProjectFromResourceAttribute)
	if s.TraceDelay > 0 {
		time.Sleep(s.TraceDelay)
	}
	mapping.MapTraceRequest(req, s.OutputStore)
	if s.FileExporter != nil {
		if err := s.FileExporter.WriteTraces(req); err != nil {
			s.Logger.Warn("failed to write trace export file", "error", err)
		}
	}
	out, err := proto.Marshal(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.forward(w, r.Context(), "/v1/traces", out)
}

func (s Server) logs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	payload, err := readBody(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req := &collectorlogsv1.ExportLogsServiceRequest{}
	if err := unmarshalOTLP(r.Header.Get("Content-Type"), payload, req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	filterLogsByService(req, s.ServiceNameFilter)
	if len(req.GetResourceLogs()) == 0 {
		writeEmptyExportResponse(w, "/v1/logs")
		return
	}
	addResourceAttributesToLogs(req, s.ResourceAttributes)
	addProjectAttributesFromResourceToLogs(req, s.ProjectFromResourceAttribute)
	outputs := s.OutputStore.IngestLogRequest(req)
	if outputs > 0 {
		s.Logger.Debug("captured api body payloads", "count", outputs)
	}
	if s.FileExporter != nil {
		if err := s.FileExporter.WriteLogs(req); err != nil {
			s.Logger.Warn("failed to write log export file", "error", err)
		}
	}
	out, err := proto.Marshal(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !s.ForwardLogs {
		writeEmptyExportResponse(w, "/v1/logs")
		return
	}
	s.forward(w, r.Context(), "/v1/logs", out)
}

func (s Server) forward(w http.ResponseWriter, ctx context.Context, path string, payload []byte) {
	if !shouldForward(s.ExporterEndpoint) {
		writeEmptyExportResponse(w, path)
		return
	}
	target := downstreamURL(s.ExporterEndpoint, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	for key, value := range s.ExporterHeaders {
		req.Header.Set(key, value)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		s.Logger.Info("forwarded telemetry", "path", path, "status", resp.StatusCode)
	} else {
		s.Logger.Warn("downstream rejected telemetry", "path", path, "status", resp.StatusCode)
	}

	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func shouldForward(endpoint string) bool {
	switch strings.ToLower(strings.TrimSpace(endpoint)) {
	case "", "none", "off", "false":
		return false
	default:
		return true
	}
}

func writeEmptyExportResponse(w http.ResponseWriter, path string) {
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(emptyExportResponse(path))
}

func readBody(r *http.Request) ([]byte, error) {
	var reader io.Reader = r.Body
	if strings.EqualFold(r.Header.Get("Content-Encoding"), "gzip") {
		gzipReader, err := gzip.NewReader(r.Body)
		if err != nil {
			return nil, err
		}
		defer gzipReader.Close()
		reader = gzipReader
	}
	defer r.Body.Close()
	return io.ReadAll(reader)
}

func unmarshalOTLP(contentType string, payload []byte, msg proto.Message) error {
	if strings.Contains(contentType, "json") {
		return protojson.Unmarshal(payload, msg)
	}
	return proto.Unmarshal(payload, msg)
}

func downstreamURL(endpoint string, path string) string {
	base := strings.TrimRight(endpoint, "/")
	if strings.HasSuffix(base, path) {
		return base
	}
	return base + path
}

func emptyExportResponse(path string) []byte {
	switch path {
	case "/v1/traces":
		out, _ := proto.Marshal(&collectortracev1.ExportTraceServiceResponse{})
		return out
	case "/v1/logs":
		out, _ := proto.Marshal(&collectorlogsv1.ExportLogsServiceResponse{})
		return out
	default:
		return []byte(fmt.Sprintf("unsupported path %s", path))
	}
}
