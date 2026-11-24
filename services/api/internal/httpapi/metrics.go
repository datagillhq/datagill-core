package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
)

// unmatchedRoute labels requests that matched no route. Using the raw path instead would
// let anyone create unbounded label values by requesting random URLs.
const unmatchedRoute = "unmatched"

type httpMetrics struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

func newHTTPMetrics(reg prometheus.Registerer) (*httpMetrics, error) {
	m := &httpMetrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "datagill_" + ServiceName + "_http_requests_total",
			Help: "HTTP requests, by method, route and status code.",
		}, []string{"method", "handler", "status_code"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "datagill_" + ServiceName + "_http_request_duration_seconds",
			Help:    "HTTP request duration in seconds, by method and route.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "handler"}),
	}
	for _, c := range []prometheus.Collector{m.requests, m.duration} {
		if err := reg.Register(c); err != nil {
			return nil, fmt.Errorf("register collector: %w", err)
		}
	}
	return m, nil
}

// middleware records each request, labeled by chi's route pattern so
// /v1/integrations/{key} is one series, not one per key.
func (m *httpMetrics) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := chimiddleware.NewWrapResponseWriter(w, r.ProtoMajor)

		next.ServeHTTP(ww, r)

		route := chi.RouteContext(r.Context()).RoutePattern()
		if route == "" {
			route = unmatchedRoute
		}
		status := ww.Status()
		if status == 0 {
			status = http.StatusOK // handler wrote nothing; net/http sends 200
		}
		m.requests.WithLabelValues(r.Method, route, strconv.Itoa(status)).Inc()
		m.duration.WithLabelValues(r.Method, route).Observe(time.Since(start).Seconds())
	})
}
