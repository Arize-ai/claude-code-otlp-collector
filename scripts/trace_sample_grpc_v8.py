"""
Arize SDK v8 – Trace sample script (OTLP over gRPC).

Sends a single sample trace to the Arize OTLP gRPC endpoint. Supports:
  - Single endpoint: one host for API, OTLP, and Flight (use SINGLE_HOST).
  - Multi endpoint: separate hosts for API, OTLP, and Flight (see instructions below).

Install:
  pip install opentelemetry-api opentelemetry-sdk opentelemetry-exporter-otlp-proto-grpc

Required environment variables:
  SPACE_ID   – Your Arize space ID (from space settings).
  API_KEY    – Your Arize API key (developer key from space settings).

Optional (single endpoint):
  SINGLE_HOST – Host used for all endpoints (API, OTLP, Flight). When set, the script
                uses this as the OTLP gRPC endpoint; no other host/endpoint args needed.

Optional (multi endpoint):
  ARIZE_OTLP_HOST      – OTLP host (default: otlp.arize.com).
  ARIZE_OTLP_GRPC_PORT – OTLP gRPC port (default: 443).

TLS / CA certificate:
  - Single cert: set ARIZE_SSL_CA_CERT to the path to your .crt/.pem file.
  - Multiple certs: set ARIZE_SSL_CA_CERT to a path list separated by colons
    (Unix) or semicolons (Windows), e.g.:
    ARIZE_SSL_CA_CERT="/path/cert1.crt:/path/cert2.crt:/path/cert3.crt:/path/cert4.crt"
    The script merges them into one CA bundle for the OTLP exporter.
  Alternatively, concatenate all certs into one .pem file and point
  ARIZE_SSL_CA_CERT at that file.
  The script sets REQUESTS_CA_BUNDLE and SSL_CERT_FILE before any imports so
  the OTLP exporter uses your cert(s). To disable verification (insecure; dev only):
  ARIZE_REQUEST_VERIFY=false (set OTEL_EXPORTER_OTLP_TRACES_INSECURE=true for gRPC).

Usage

  Single endpoint (recommended for on-prem / single-host deployments):
    export SPACE_ID="your-space-id"
    export API_KEY="your-api-key"
    export SINGLE_HOST="arize-app.mydomain.com"
    python trace_sample_grpc_v8.py

    # Or pass host as first argument (same as datasets_sample_v8):
    python trace_sample_grpc_v8.py arize-app.mydomain.com

  Multi endpoint (separate hosts for API, OTLP, and Flight):
    export SPACE_ID="your-space-id"
    export API_KEY="your-api-key"
    export ARIZE_OTLP_HOST="otlp.mydomain.com"
    export ARIZE_OTLP_GRPC_PORT="443"
    python trace_sample_grpc_v8.py
"""

import os
import re
import sys
import tempfile


def _resolve_ca_bundle():
    raw = os.environ.get("ARIZE_SSL_CA_CERT")
    if not raw:
        return None
    paths = [p.strip() for p in raw.split(os.pathsep) if p.strip()]
    if not paths:
        return None
    valid = [os.path.abspath(p) for p in paths if os.path.isfile(p)]
    if not valid:
        return None
    if len(valid) == 1:
        return valid[0]
    with tempfile.NamedTemporaryFile(mode="w", suffix=".pem", delete=False) as f:
        for path in valid:
            with open(path) as cf:
                content = cf.read()
            f.write(content)
            if content and content[-1] != "\n":
                f.write("\n")
        return f.name


# CA cert(s) for self-signed TLS – set before importing OpenTelemetry (only when ARIZE_SSL_CA_CERT is set)
_ca_bundle = _resolve_ca_bundle()
if _ca_bundle:
    os.environ["REQUESTS_CA_BUNDLE"] = _ca_bundle
    os.environ["SSL_CERT_FILE"] = _ca_bundle

from opentelemetry import trace
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import SimpleSpanProcessor
from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import OTLPSpanExporter
from opentelemetry.sdk.resources import Resource

# -----------------------------------------------------------------------------
# Configuration
# -----------------------------------------------------------------------------
SPACE_ID = os.environ.get("SPACE_ID", "").strip()
API_KEY = os.environ.get("API_KEY", "").strip()
if not SPACE_ID or not API_KEY:
    print(
        "Error: SPACE_ID and API_KEY must be set in the environment.\n"
        "Get them from your Arize space settings.",
        file=sys.stderr,
    )
    sys.exit(1)

def _normalize_single_host(host: str) -> str:
    """Return host part only, no scheme, no trailing path."""
    if not host:
        return ""
    s = host.strip()
    s = re.sub(r"^https?://", "", s, flags=re.IGNORECASE)
    s = s.split("/")[0].split("?")[0]
    return s


# Single endpoint: one host for API, OTLP, and Flight.
# Set via env SINGLE_HOST or pass as first command-line argument.
_raw_single = os.environ.get("SINGLE_HOST", "").strip() or (
    sys.argv[1] if len(sys.argv) > 1 else None
)
SINGLE_HOST = _normalize_single_host(_raw_single) if _raw_single else None


def get_otlp_grpc_endpoint() -> str:
    """Build OTLP gRPC endpoint URL from SINGLE_HOST or ARIZE_OTLP_HOST."""
    if SINGLE_HOST:
        return f"https://{SINGLE_HOST}:443"
    otlp_host = os.environ.get("ARIZE_OTLP_HOST", "otlp.arize.com").strip()
    otlp_host = _normalize_single_host(otlp_host) or otlp_host
    port = os.environ.get("ARIZE_OTLP_GRPC_PORT", "443").strip()
    if ":" in otlp_host:
        return f"https://{otlp_host}" if not otlp_host.startswith("http") else otlp_host
    return f"https://{otlp_host}:{port}"


# Set headers for authentication (Space and API key)
headers = f"arize-space-id={SPACE_ID},authorization={API_KEY}"
os.environ["OTEL_EXPORTER_OTLP_TRACES_HEADERS"] = headers

# Optional: TLS certificate for OTLP gRPC exporter (uses resolved _ca_bundle when set)
if _ca_bundle:
    os.environ["OTEL_EXPORTER_OTLP_CERTIFICATE"] = _ca_bundle

# Resource attributes for the application (model name/version in Arize)
trace_attributes = {
    "model_id": "test-trace-grpc",
    "model_version": "v1",
}

server_address = get_otlp_grpc_endpoint()

# Set up the tracer provider and OTLP gRPC exporter
trace.set_tracer_provider(
    TracerProvider(resource=Resource(attributes=trace_attributes))
)
otlp_exporter = OTLPSpanExporter(endpoint=server_address)
trace.get_tracer_provider().add_span_processor(SimpleSpanProcessor(otlp_exporter))
tracer = trace.get_tracer(__name__)


def send_single_trace():
    with tracer.start_as_current_span("test-span") as span:
        print("Sending single trace to the OpenTelemetry gRPC endpoint...")
        span.set_attribute("openinference.span.kind", "LLM")
        span.set_attribute("test.attribute", "demo")
        span.add_event("This is a dummy event.")


if __name__ == "__main__":
    if SINGLE_HOST:
        print(f"Using single endpoint (SINGLE_HOST): {SINGLE_HOST}")
    else:
        print("Using multi-endpoint (ARIZE_OTLP_HOST from env or default).")
    print(f"OTLP gRPC endpoint: {server_address}")
    send_single_trace()
    print("Trace sent.")
