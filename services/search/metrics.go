package main

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Package-level metrics registered once at startup via promauto.
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

	dbQueryDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "datagill_db_query_duration_seconds",
		Help:    "Database query duration in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"query_name"})
)
