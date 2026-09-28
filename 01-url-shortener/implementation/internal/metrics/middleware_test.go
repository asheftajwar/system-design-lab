package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestMiddlewareRecordsRequest(t *testing.T) {
	m := New()

	handler := Middleware(m)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte("created"))
		}),
	)

	req := httptest.NewRequest(http.MethodPost, "/v1/urls", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf(
			"expected status %d, got %d",
			http.StatusCreated,
			rec.Code,
		)
	}

	count := testutil.ToFloat64(
		m.HTTPRequestsTotal.WithLabelValues(
			http.MethodPost,
			"/v1/urls",
			"201",
		),
	)

	if count != 1 {
		t.Fatalf("expected 1 recorded request, got %v", count)
	}
}

func TestMiddlewareUsesRoutePattern(t *testing.T) {
	m := New()

	handler := Middleware(m)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusFound)
		}),
	)

	req := httptest.NewRequest(http.MethodGet, "/abc123", nil)
	req.Pattern = "/{code}"

	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	count := testutil.ToFloat64(
		m.HTTPRequestsTotal.WithLabelValues(
			http.MethodGet,
			"/{code}",
			"302",
		),
	)

	if count != 1 {
		t.Fatalf("expected 1 recorded request, got %v", count)
	}
}

func TestMiddlewareDefaultsToOK(t *testing.T) {
	m := New()

	handler := Middleware(m)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("ok"))
		}),
	)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf(
			"expected status %d, got %d",
			http.StatusOK,
			rec.Code,
		)
	}

	count := testutil.ToFloat64(
		m.HTTPRequestsTotal.WithLabelValues(
			http.MethodGet,
			"/health",
			"200",
		),
	)

	if count != 1 {
		t.Fatalf("expected 1 recorded request, got %v", count)
	}
}
