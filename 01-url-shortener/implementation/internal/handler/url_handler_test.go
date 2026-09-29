package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/asheftajwar/system-design-lab/01-url-shortener/internal/domain"
	"github.com/asheftajwar/system-design-lab/01-url-shortener/internal/repository"
	"github.com/asheftajwar/system-design-lab/01-url-shortener/internal/service"
)

type fakeURLRepository struct {
	createFunc func(ctx context.Context, url *domain.URL) error

	getByIDFunc func(
		ctx context.Context,
		id int64,
	) (*domain.URL, error)

	getByAliasFunc func(
		ctx context.Context,
		alias string,
	) (*domain.URL, error)
}

type fakeAnalyticsRepository struct {
	getAnalyticsFunc func(
		ctx context.Context,
		urlID int64,
	) (*repository.URLAnalytics, error)

	recordRedirectFunc func(
		ctx context.Context,
		urlID int64,
		accessedAt time.Time,
	) error
}

func (f *fakeAnalyticsRepository) RecordRedirect(
	ctx context.Context,
	urlID int64,
	accessedAt time.Time,
) error {
	if f.recordRedirectFunc != nil {
		return f.recordRedirectFunc(ctx, urlID, accessedAt)
	}

	return nil
}

func (f *fakeAnalyticsRepository) GetAnalytics(
	ctx context.Context,
	urlID int64,
) (*repository.URLAnalytics, error) {
	if f.getAnalyticsFunc != nil {
		return f.getAnalyticsFunc(ctx, urlID)
	}

	return nil, repository.ErrNotFound
}

func (f *fakeURLRepository) Create(
	ctx context.Context,
	url *domain.URL,
) error {
	return f.createFunc(ctx, url)
}

func (f *fakeURLRepository) GetByID(
	ctx context.Context,
	id int64,
) (*domain.URL, error) {
	if f.getByIDFunc != nil {
		return f.getByIDFunc(ctx, id)
	}

	return nil, repository.ErrNotFound
}

func (f *fakeURLRepository) GetByAlias(
	ctx context.Context,
	alias string,
) (*domain.URL, error) {
	if f.getByAliasFunc != nil {
		return f.getByAliasFunc(ctx, alias)
	}

	return nil, repository.ErrNotFound
}

func TestURLHandlerCreateURL(t *testing.T) {
	repo := &fakeURLRepository{
		createFunc: func(ctx context.Context, url *domain.URL) error {
			url.ID = 1234567
			url.CreatedAt = time.Now()

			return nil
		},
	}

	svc := service.NewURLService(repo, "http://localhost:8080")
	handler := NewURLHandler(svc)

	body := `{
		"url": "https://example.com/very/long/path"
	}`

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/urls",
		strings.NewReader(body),
	)

	req.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()

	handler.CreateURL(recorder, req)

	if recorder.Code != http.StatusCreated {
		t.Fatalf(
			"expected status %d, got %d",
			http.StatusCreated,
			recorder.Code,
		)
	}

	expected := `{"code":"5BAN","short_url":"http://localhost:8080/5BAN","expires_at":null}`

	if strings.TrimSpace(recorder.Body.String()) != expected {
		t.Fatalf(
			"expected body %s, got %s",
			expected,
			strings.TrimSpace(recorder.Body.String()),
		)
	}
}

func TestURLHandlerRejectsInvalidJSON(t *testing.T) {
	repo := &fakeURLRepository{
		createFunc: func(ctx context.Context, url *domain.URL) error {
			t.Fatal("repository should not be called")

			return nil
		},
	}

	svc := service.NewURLService(repo, "http://localhost:8080")
	handler := NewURLHandler(svc)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/urls",
		strings.NewReader(`{"url":`),
	)

	recorder := httptest.NewRecorder()

	handler.CreateURL(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf(
			"expected status %d, got %d",
			http.StatusBadRequest,
			recorder.Code,
		)
	}
}

func TestURLHandlerRejectsInvalidURL(t *testing.T) {
	repo := &fakeURLRepository{
		createFunc: func(ctx context.Context, url *domain.URL) error {
			t.Fatal("repository should not be called")

			return nil
		},
	}

	svc := service.NewURLService(repo, "http://localhost:8080")
	handler := NewURLHandler(svc)

	body := `{
		"url": "ftp://example.com"
	}`

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/urls",
		strings.NewReader(body),
	)

	recorder := httptest.NewRecorder()

	handler.CreateURL(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf(
			"expected status %d, got %d",
			http.StatusBadRequest,
			recorder.Code,
		)
	}
}

func TestURLHandlerPropagatesInternalError(t *testing.T) {
	expectedErr := errors.New("database unavailable")

	repo := &fakeURLRepository{
		createFunc: func(ctx context.Context, url *domain.URL) error {
			return expectedErr
		},
	}

	svc := service.NewURLService(repo, "http://localhost:8080")
	handler := NewURLHandler(svc)

	body := `{
		"url": "https://example.com"
	}`

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/urls",
		strings.NewReader(body),
	)

	recorder := httptest.NewRecorder()

	handler.CreateURL(recorder, req)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf(
			"expected status %d, got %d",
			http.StatusInternalServerError,
			recorder.Code,
		)
	}
}

func TestURLHandlerRedirect(t *testing.T) {
	alias := "docs"

	repo := &fakeURLRepository{
		getByAliasFunc: func(
			ctx context.Context,
			value string,
		) (*domain.URL, error) {
			if value != alias {
				t.Fatalf(
					"expected alias %q, got %q",
					alias,
					value,
				)
			}

			return &domain.URL{
				ID:          123,
				OriginalURL: "https://example.com/docs",
				CustomAlias: &alias,
				CreatedAt:   time.Now(),
			}, nil
		},
	}

	svc := service.NewURLService(repo, "http://localhost:8080")
	handler := NewURLHandler(svc)

	req := httptest.NewRequest(
		http.MethodGet,
		"/docs",
		nil,
	)

	req.SetPathValue("code", "docs")

	recorder := httptest.NewRecorder()

	handler.Redirect(recorder, req)

	if recorder.Code != http.StatusFound {
		t.Fatalf(
			"expected status %d, got %d",
			http.StatusFound,
			recorder.Code,
		)
	}

	location := recorder.Header().Get("Location")

	if location != "https://example.com/docs" {
		t.Fatalf(
			"expected Location %q, got %q",
			"https://example.com/docs",
			location,
		)
	}
}

func TestURLHandlerRedirectExpired(t *testing.T) {
	expiredAt := time.Now().Add(-time.Hour)
	alias := "expired"

	repo := &fakeURLRepository{
		getByAliasFunc: func(
			ctx context.Context,
			value string,
		) (*domain.URL, error) {
			return &domain.URL{
				ID:          123,
				OriginalURL: "https://example.com/expired",
				CustomAlias: &alias,
				ExpiresAt:   &expiredAt,
				CreatedAt:   time.Now().Add(-2 * time.Hour),
			}, nil
		},
	}

	svc := service.NewURLService(repo, "http://localhost:8080")
	handler := NewURLHandler(svc)

	req := httptest.NewRequest(
		http.MethodGet,
		"/expired",
		nil,
	)

	req.SetPathValue("code", "expired")

	recorder := httptest.NewRecorder()

	handler.Redirect(recorder, req)

	if recorder.Code != http.StatusGone {
		t.Fatalf(
			"expected status %d, got %d",
			http.StatusGone,
			recorder.Code,
		)
	}
}

func TestURLHandlerRedirectNotFound(t *testing.T) {
	repo := &fakeURLRepository{
		getByAliasFunc: func(
			ctx context.Context,
			value string,
		) (*domain.URL, error) {
			return nil, repository.ErrNotFound
		},
		getByIDFunc: func(
			ctx context.Context,
			id int64,
		) (*domain.URL, error) {
			return nil, repository.ErrNotFound
		},
	}

	svc := service.NewURLService(repo, "http://localhost:8080")
	handler := NewURLHandler(svc)

	req := httptest.NewRequest(
		http.MethodGet,
		"/does-not-exist",
		nil,
	)

	req.SetPathValue("code", "does-not-exist")

	recorder := httptest.NewRecorder()

	handler.Redirect(recorder, req)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf(
			"expected status %d, got %d",
			http.StatusNotFound,
			recorder.Code,
		)
	}
}

func TestURLHandlerCreateURLDuplicateAlias(t *testing.T) {
	repo := &fakeURLRepository{
		createFunc: func(ctx context.Context, url *domain.URL) error {
			return repository.ErrDuplicateAlias
		},
	}

	svc := service.NewURLService(repo, "http://localhost:8080")
	handler := NewURLHandler(svc)

	body := `{
		"url": "https://example.com/docs",
		"custom_alias": "docs"
	}`

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/urls",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()

	handler.CreateURL(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf(
			"CreateURL() status = %d, want %d",
			rec.Code,
			http.StatusConflict,
		)
	}

	if !strings.Contains(rec.Body.String(), "custom alias already exists") {
		t.Fatalf(
			"expected duplicate alias error, got %q",
			rec.Body.String(),
		)
	}
}

func TestURLHandlerRedirectOverflowCode(t *testing.T) {
	repo := &fakeURLRepository{
		getByAliasFunc: func(ctx context.Context, alias string) (*domain.URL, error) {
			return nil, repository.ErrNotFound
		},
		getByIDFunc: func(ctx context.Context, id int64) (*domain.URL, error) {
			t.Fatal("GetByID should not be called for an overflowing Base62 code")
			return nil, nil
		},
	}

	svc := service.NewURLService(repo, "http://localhost:8080")
	h := NewURLHandler(svc)

	req := httptest.NewRequest(
		http.MethodGet,
		"/AzL8n0Y58m8",
		nil,
	)
	req.SetPathValue("code", "AzL8n0Y58m8")

	rec := httptest.NewRecorder()

	h.Redirect(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf(
			"Redirect() status = %d, want %d",
			rec.Code,
			http.StatusNotFound,
		)
	}
}

func TestCreateURLMissingURL(t *testing.T) {
	repo := &fakeURLRepository{
		getByAliasFunc: func(ctx context.Context, alias string) (*domain.URL, error) {
			return nil, repository.ErrNotFound
		},
		getByIDFunc: func(ctx context.Context, id int64) (*domain.URL, error) {
			t.Fatal("GetByID should not be called for an overflowing Base62 code")
			return nil, nil
		},
	}
	svc := service.NewURLService(repo, "http://localhost:8080")
	handler := NewURLHandler(svc)

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/urls",
		strings.NewReader(`{}`),
	)
	req.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()

	handler.CreateURL(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}

func TestCreateURLRejectsTrailingJSON(t *testing.T) {
	repo := &fakeURLRepository{
		getByAliasFunc: func(ctx context.Context, alias string) (*domain.URL, error) {
			return nil, repository.ErrNotFound
		},
		getByIDFunc: func(ctx context.Context, id int64) (*domain.URL, error) {
			t.Fatal("GetByID should not be called for an overflowing Base62 code")
			return nil, nil
		},
	}
	svc := service.NewURLService(repo, "http://localhost:8080")
	handler := NewURLHandler(svc)

	body := `{"url":"https://example.com"} {"extra":true}`

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/urls",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()

	handler.CreateURL(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}

func TestCreateURLRejectsOversizedBody(t *testing.T) {
	repo := &fakeURLRepository{
		getByAliasFunc: func(ctx context.Context, alias string) (*domain.URL, error) {
			return nil, repository.ErrNotFound
		},
		getByIDFunc: func(ctx context.Context, id int64) (*domain.URL, error) {
			t.Fatal("GetByID should not be called for an overflowing Base62 code")
			return nil, nil
		},
	}

	svc := service.NewURLService(repo, "http://localhost:8080")
	handler := NewURLHandler(svc)

	oversizedURL := "https://example.com/" + strings.Repeat("a", maxCreateURLBodySize)

	body := `{"url":"` + oversizedURL + `"}`

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/urls",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()

	handler.CreateURL(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}

func TestURLHandlerGetMetadata(t *testing.T) {
	createdAt := time.Date(
		2026, 9, 29, 12, 0, 0, 0,
		time.UTC,
	)

	lastAccessedAt := time.Date(
		2026, 9, 29, 13, 30, 0, 0,
		time.UTC,
	)

	alias := "docs"

	urlRepo := &fakeURLRepository{
		getByAliasFunc: func(
			ctx context.Context,
			value string,
		) (*domain.URL, error) {
			if value != alias {
				t.Fatalf(
					"expected alias %q, got %q",
					alias,
					value,
				)
			}

			return &domain.URL{
				ID:          123,
				OriginalURL: "https://example.com/docs",
				CustomAlias: &alias,
				CreatedAt:   createdAt,
				ExpiresAt:   nil,
			}, nil
		},
	}

	analyticsRepo := &fakeAnalyticsRepository{
		getAnalyticsFunc: func(
			ctx context.Context,
			urlID int64,
		) (*repository.URLAnalytics, error) {
			if urlID != 123 {
				t.Fatalf(
					"expected URL ID 123, got %d",
					urlID,
				)
			}

			return &repository.URLAnalytics{
				URLID:          123,
				RedirectCount:  7,
				LastAccessedAt: &lastAccessedAt,
			}, nil
		},
	}

	svc := service.NewURLService(
		urlRepo,
		"http://localhost:8080",
	).WithAnalyticsRepository(analyticsRepo)

	handler := NewURLHandler(svc)

	req := httptest.NewRequest(
		http.MethodGet,
		"/v1/urls/docs",
		nil,
	)

	req.SetPathValue("code", "docs")

	recorder := httptest.NewRecorder()

	handler.GetMetadata(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf(
			"expected status %d, got %d",
			http.StatusOK,
			recorder.Code,
		)
	}

	expected := `{
		"code":"docs",
		"original_url":"https://example.com/docs",
		"created_at":"2026-09-29T12:00:00Z",
		"expires_at":null,
		"redirect_count":7,
		"last_accessed_at":"2026-09-29T13:30:00Z"
	}`

	assertJSONEqual(t, expected, recorder.Body.String())
}

func TestURLHandlerGetMetadataWithoutAnalytics(t *testing.T) {
	createdAt := time.Date(
		2026, 9, 29, 12, 0, 0, 0,
		time.UTC,
	)

	alias := "new-url"

	urlRepo := &fakeURLRepository{
		getByAliasFunc: func(
			ctx context.Context,
			value string,
		) (*domain.URL, error) {
			return &domain.URL{
				ID:          456,
				OriginalURL: "https://example.com/new",
				CustomAlias: &alias,
				CreatedAt:   createdAt,
			}, nil
		},
	}

	analyticsRepo := &fakeAnalyticsRepository{
		getAnalyticsFunc: func(
			ctx context.Context,
			urlID int64,
		) (*repository.URLAnalytics, error) {
			return nil, repository.ErrNotFound
		},
	}

	svc := service.NewURLService(
		urlRepo,
		"http://localhost:8080",
	).WithAnalyticsRepository(analyticsRepo)

	handler := NewURLHandler(svc)

	req := httptest.NewRequest(
		http.MethodGet,
		"/v1/urls/new-url",
		nil,
	)

	req.SetPathValue("code", "new-url")

	recorder := httptest.NewRecorder()

	handler.GetMetadata(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf(
			"expected status %d, got %d",
			http.StatusOK,
			recorder.Code,
		)
	}

	expected := `{
		"code":"new-url",
		"original_url":"https://example.com/new",
		"created_at":"2026-09-29T12:00:00Z",
		"expires_at":null,
		"redirect_count":0,
		"last_accessed_at":null
	}`

	assertJSONEqual(t, expected, recorder.Body.String())
}

func TestURLHandlerGetMetadataNotFound(t *testing.T) {
	urlRepo := &fakeURLRepository{
		getByAliasFunc: func(
			ctx context.Context,
			alias string,
		) (*domain.URL, error) {
			return nil, repository.ErrNotFound
		},
		getByIDFunc: func(
			ctx context.Context,
			id int64,
		) (*domain.URL, error) {
			return nil, repository.ErrNotFound
		},
	}

	analyticsRepo := &fakeAnalyticsRepository{}

	svc := service.NewURLService(
		urlRepo,
		"http://localhost:8080",
	).WithAnalyticsRepository(analyticsRepo)

	handler := NewURLHandler(svc)

	req := httptest.NewRequest(
		http.MethodGet,
		"/v1/urls/does-not-exist",
		nil,
	)

	req.SetPathValue("code", "does-not-exist")

	recorder := httptest.NewRecorder()

	handler.GetMetadata(recorder, req)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf(
			"expected status %d, got %d",
			http.StatusNotFound,
			recorder.Code,
		)
	}
}

func TestURLHandlerGetMetadataAnalyticsError(t *testing.T) {
	expectedErr := errors.New("analytics database unavailable")

	alias := "docs"

	urlRepo := &fakeURLRepository{
		getByAliasFunc: func(
			ctx context.Context,
			value string,
		) (*domain.URL, error) {
			return &domain.URL{
				ID:          123,
				OriginalURL: "https://example.com/docs",
				CustomAlias: &alias,
				CreatedAt:   time.Now(),
			}, nil
		},
	}

	analyticsRepo := &fakeAnalyticsRepository{
		getAnalyticsFunc: func(
			ctx context.Context,
			urlID int64,
		) (*repository.URLAnalytics, error) {
			return nil, expectedErr
		},
	}

	svc := service.NewURLService(
		urlRepo,
		"http://localhost:8080",
	).WithAnalyticsRepository(analyticsRepo)

	handler := NewURLHandler(svc)

	req := httptest.NewRequest(
		http.MethodGet,
		"/v1/urls/docs",
		nil,
	)

	req.SetPathValue("code", "docs")

	recorder := httptest.NewRecorder()

	handler.GetMetadata(recorder, req)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf(
			"expected status %d, got %d",
			http.StatusInternalServerError,
			recorder.Code,
		)
	}
}

func assertJSONEqual(t *testing.T, expected, actual string) {
	t.Helper()

	var expectedJSON any
	if err := json.Unmarshal([]byte(expected), &expectedJSON); err != nil {
		t.Fatalf("invalid expected JSON: %v", err)
	}

	var actualJSON any
	if err := json.Unmarshal([]byte(actual), &actualJSON); err != nil {
		t.Fatalf("invalid actual JSON: %v", err)
	}

	if !reflect.DeepEqual(expectedJSON, actualJSON) {
		t.Fatalf(
			"JSON mismatch:\nexpected: %s\nactual:   %s",
			expected,
			actual,
		)
	}
}
