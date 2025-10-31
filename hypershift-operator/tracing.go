package main

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"
	"k8s.io/component-base/tracing"
	tracingapi "k8s.io/component-base/tracing/api/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const (
	NodeNameEnvVar         = "NODE_NAME"
	OTLPEndpointPortEnvVar = "OTEL_EXPORTER_OTLP_ENDPOINT_PORT"

	DefaultOTLPPort = "4317"
	DefaultOTLPHost = "localhost"

	// TracingSamplingRateEnvVar is the environment variable for sampling rate per million
	TracingSamplingRateEnvVar = "OTEL_TRACING_SAMPLING_RATE_PER_MILLION"

	// DefaultSamplingRate samples all traces by default (1000000 = 100%)
	DefaultSamplingRate = 1000000
)

// setupTracing initializes the OpenTelemetry tracer provider for the hypershift operator.
// It uses the k8s.io/component-base/tracing utilities which follow Kubernetes standards.
// The tracer provider will export traces to an OTLP gRPC endpoint (typically a local agent).
func setupTracing(ctx context.Context, mgr manager.Manager) (tracing.TracerProvider, error) {
	log := mgr.GetLogger()

	// Get OTLP endpoint from environment or use default
	host := os.Getenv(NodeNameEnvVar)
	if host == "" {
		log.Info("Node name env var not found, default to: %s", DefaultOTLPHost)
		host = DefaultOTLPHost
	}

	port := os.Getenv(OTLPEndpointPortEnvVar)
	if port == "" {
		log.Info("OTLP collector port env var not found, default to: %s", DefaultOTLPPort)
		port = DefaultOTLPPort
	}

	endpoint := fmt.Sprintf("%s:%s", host, port)

	// Get sampling rate from environment or use default
	samplingRate := int32(DefaultSamplingRate)
	if samplingRateStr := os.Getenv(TracingSamplingRateEnvVar); samplingRateStr != "" {
		if rate, err := strconv.ParseInt(samplingRateStr, 10, 32); err == nil {
			samplingRate = int32(rate)
		}
	}

	// Create tracing configuration
	tracingConfig := &tracingapi.TracingConfiguration{
		Endpoint:               &endpoint,
		SamplingRatePerMillion: &samplingRate,
	}

	// Create a new client to get operator information
	tmpClient, err := crclient.New(mgr.GetConfig(), crclient.Options{Scheme: mgr.GetScheme()})
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary client for tracing setup: %w", err)
	}

	// Get operator image information for resource attributes
	image, imageID, err := getOperatorImage(tmpClient)
	if err != nil {
		log.Error(err, "failed to get operator image for tracing, setting to unknown")
		image = "unknown"
		imageID = "unknown"
	}

	// Create resource options with service information
	resourceOpts := []resource.Option{
		resource.WithAttributes(
			semconv.ServiceName("hypershift-operator"),
			semconv.ServiceVersion(hypershiftVersion),
			semconv.ContainerImageName(image),
			semconv.ContainerImageID(imageID),
		),
		resource.WithHost(),
		resource.WithOS(),
		resource.WithProcess(),
	}

	// Create the tracer provider using k8s.io/component-base/tracing
	tp, err := tracing.NewProvider(ctx, tracingConfig, nil, resourceOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to create tracer provider: %w", err)
	}

	log.Info("Tracing configured successfully",
		"endpoint", endpoint,
		"samplingRate", samplingRate,
		"serviceName", "hypershift-operator",
		"version", hypershiftVersion,
	)

	return tp, nil
}
