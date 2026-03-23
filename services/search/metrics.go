package main

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Package-level metrics registered once at startup via promauto.
// Phase 2B will add a MetricsRecorder interface for per-query DB metrics
// that can be injected into the repository and handlers.
var (
	httpRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "datagill_http_requests_total",
		Help: "Total number of HTTP requests.",
	}, []string{"method", "handler", "status_code"})

	httpRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "datagill_http_request_duration_seconds",
		Help:    "HTTP request duration in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "handler"})
)
