# OpenTelemetry Tracing in HyperShift Operator

## How It Works

### Architecture Flow

```
┌─────────────────────┐
│ Hypershift Operator │
│                     │
│  1. Reconcile()     │──┐
│     starts          │  │ Create span with
│                     │  │ context propagation
│  2. tracing.Start() │◄─┘
│     called          │
│                     │
│  3. Span events &   │
│     attributes      │
│     recorded        │
│                     │
│  4. Reconcile()     │
│     completes       │
│                     │
│  5. span.End()      │──┐
└─────────────────────┘  │
                         │ Span sent to exporter
                         ▼
┌─────────────────────────────────────┐
│ OTLP Exporter (in operator process) │
└─────────────────────────────────────┘
                         │
                         │ OTLP/gRPC (port 4317)
                         ▼
┌─────────────────────────────────────┐
│ OTel Agent DaemonSet                │
│ (on same node as operator)          │
│                                     │
│ Endpoint: localhost:4317            │
└─────────────────────────────────────┘
                         │
                         │ OTLP/gRPC (port 4317)
                         ▼
┌─────────────────────────────────────┐
│ OTel Collector Deployment           │
│ (otel-collector.default:4317)       │
│                                     │
│ - Receives traces from all agents   │
│ - Processes (filters, samples, etc) │
│ - Exports to backend                │
└─────────────────────────────────────┘
                         │
                         │ OTLP/HTTP or OTLP/gRPC
                         ▼
┌─────────────────────────────────────┐
│ Your Observability Backend          │
│ (Jaeger, Tempo, etc.)               │
└─────────────────────────────────────┘
```

### What Gets Traced

Every time the hypershift-operator reconciles a HostedCluster, it:

1. **Creates a span** named `HostedCluster.Reconcile`
2. **Adds attributes**:
   - `namespace`: The namespace of the HostedCluster
   - `name`: The name of the HostedCluster
   - `platform`: AWS, Azure, KubeVirt, etc.
   - `clusterID`: The unique cluster ID
3. **Records events**:
   - "found hostedcluster" - when the cluster is retrieved
   - "hostedcluster not found" - if it doesn't exist
4. **Records errors** if reconciliation fails
5. **Ends the span** with timing information

### Context Propagation

The key is that `ctx` carries the span throughout the reconcile operation. If you call other functions with this context, they can create **child spans** that show the hierarchical relationship:

```
HostedCluster.Reconcile (parent span)
├── reconcile.validateCluster (child span)
├── reconcile.ensureControlPlane (child span)
│   ├── createNamespace (grandchild span)
│   └── deployComponents (grandchild span)
└── reconcile.updateStatus (child span)
```

This creates a **trace** - a complete picture of what happened during one reconcile operation.

## Example: What You'll See on the Collector

### Collector Logs

When traces are received, you'll see logs like:

```json
{
  "level": "info",
  "ts": 1730000000.0,
  "caller": "exporterhelper/queued_retry.go:123",
  "msg": "Exporting items",
  "exporter": "otlp",
  "sent_items": 1
}
```

### Trace Data Structure (JSON representation)

Here's an example of what a single trace span looks like:

```json
{
  "resourceSpans": [{
    "resource": {
      "attributes": [
        { "key": "service.name", "value": { "stringValue": "hypershift-operator" } },
        { "key": "service.version", "value": { "stringValue": "v4.18.0-abc123" } },
        { "key": "container.image.name", "value": { "stringValue": "quay.io/hypershift/hypershift-operator:latest" } },
        { "key": "host.name", "value": { "stringValue": "ip-10-0-1-123.ec2.internal" } }
      ]
    },
    "scopeSpans": [{
      "scope": {
        "name": "k8s.io/component-base/tracing"
      },
      "spans": [{
        "traceId": "5b8aa5a2d2c872e8321cf37308d69df2",
        "spanId": "051581bf3cb55c13",
        "name": "HostedCluster.Reconcile",
        "kind": "SPAN_KIND_INTERNAL",
        "startTimeUnixNano": "1730000000000000000",
        "endTimeUnixNano": "1730000002500000000",
        "attributes": [
          { "key": "namespace", "value": { "stringValue": "clusters" } },
          { "key": "name", "value": { "stringValue": "my-cluster" } },
          { "key": "platform", "value": { "stringValue": "AWS" } },
          { "key": "clusterID", "value": { "stringValue": "a1b2c3d4-e5f6-7890-abcd-ef1234567890" } }
        ],
        "events": [
          {
            "timeUnixNano": "1730000000100000000",
            "name": "found hostedcluster",
            "attributes": [
              { "key": "platform", "value": { "stringValue": "AWS" } },
              { "key": "clusterID", "value": { "stringValue": "a1b2c3d4-e5f6-7890-abcd-ef1234567890" } }
            ]
          }
        ],
        "status": {
          "code": "STATUS_CODE_OK"
        }
      }]
    }]
  }]
}
```

### If There's an Error

If reconciliation fails, you'll see:

```json
{
  "spans": [{
    "name": "HostedCluster.Reconcile",
    "attributes": [
      { "key": "namespace", "value": { "stringValue": "clusters" } },
      { "key": "name", "value": { "stringValue": "my-cluster" } }
    ],
    "events": [
      {
        "name": "exception",
        "attributes": [
          { "key": "exception.type", "value": { "stringValue": "*errors.errorString" } },
          { "key": "exception.message", "value": { "stringValue": "failed to create control plane: context deadline exceeded" } },
          { "key": "reason", "value": { "stringValue": "ReconciliationError" } }
        ]
      }
    ],
    "status": {
      "code": "STATUS_CODE_ERROR",
      "message": "failed to create control plane: context deadline exceeded"
    }
  }]
}
```

### Querying Traces in Your Backend

Once exported to your backend (Jaeger, Tempo, etc.), you can:

1. **Search by attributes**:
   - Find all reconciliations for `namespace=clusters`
   - Find all traces for a specific cluster: `name=my-cluster`
   - Find all errors: `status.code=ERROR`

2. **View timing**:
   - See how long reconciliation took
   - Identify slow operations
   - Compare performance over time

3. **Visualize the call tree**:
   ```
   HostedCluster.Reconcile [2.5s]
   └── Took 2.5 seconds total
       Attributes: namespace=clusters, name=my-cluster, platform=AWS
       Events: found hostedcluster @ 100ms
   ```

### Example Jaeger UI View

If you're using Jaeger, the trace would look like:

```
Trace ID: 5b8aa5a2d2c872e8321cf37308d69df2
Service: hypershift-operator
Duration: 2.5s

Span: HostedCluster.Reconcile
├─ Duration: 2.5s
├─ Tags:
│  ├─ namespace: clusters
│  ├─ name: my-cluster
│  ├─ platform: AWS
│  └─ clusterID: a1b2c3d4-e5f6-7890-abcd-ef1234567890
└─ Logs:
   └─ [100ms] found hostedcluster
      ├─ platform: AWS
      └─ clusterID: a1b2c3d4-e5f6-7890-abcd-ef1234567890
```

## Configuration

The operator reads these environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `NODE_NAME` | `localhost` | Name of the node the operator is running on. Ensures telemetry is always sent to the agent in the same node. |
| `OTEL_EXPORTER_OTLP_ENDPOINT_PORT` | `4317` | OTLP gRPC endpoint (your agent) |
| `OTEL_TRACING_SAMPLING_RATE_PER_MILLION` | `1000000` (100%) | Sampling rate |

## Testing the Integration

Once configured, traces will flow: **Operator → Agent (node-name:4317) → Collector → Your Backend**

You can verify traces are being sent by:

1. **Check operator logs** for tracing initialization:
```bash
kubectl logs -n hypershift deployment/operator | grep -i "sending traces"
```

2. **Check agent metrics**:
```bash
kubectl port-forward <agent-pod> 8888:8888
curl http://localhost:8888/metrics | grep otelcol_exporter
```

3. **Check collector logs**:
```bash
kubectl logs -l component=otel-collector
```

4. **Trigger a reconciliation**:
```bash
# Create or update a HostedCluster
kubectl create -f my-hostedcluster.yaml

# Check collector metrics
kubectl port-forward svc/otel-collector 8888:8888
curl http://localhost:8888/metrics | grep otelcol_receiver_accepted_spans
# Should show: otelcol_receiver_accepted_spans{...} 1
```

## Implementation Details

The implementation uses:
- **k8s.io/component-base/tracing** - Standard Kubernetes tracing utilities
- **go.opentelemetry.io/otel** - OpenTelemetry SDK
- **OTLP gRPC exporter** - Sends traces to the local agent

Files modified:
- `hypershift-operator/tracing.go` - Tracing setup utility
- `hypershift-operator/main.go` - Tracer provider initialization
- `cmd/install/assets/hypershift_operator.go` - Add env vars to configure telemetry
- `hypershift-operator/controllers/hostedcluster/hostedcluster_controller.go` - Span instrumentation

The implementation follows the same pattern as metrics in hypershift-operator/metrics.go, using standard Kubernetes libraries instead of custom solutions.

## Sampling

The implementation uses **sampling rate of 100%** by default, so every reconciliation creates a trace. In production, you might want to reduce this to reduce overhead:

- **10% sampling**: `OTEL_TRACING_SAMPLING_RATE_PER_MILLION=100000`
- **1% sampling**: `OTEL_TRACING_SAMPLING_RATE_PER_MILLION=10000`
- **0.1% sampling**: `OTEL_TRACING_SAMPLING_RATE_PER_MILLION=1000`
