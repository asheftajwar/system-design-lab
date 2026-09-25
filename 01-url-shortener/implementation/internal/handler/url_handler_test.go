package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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

	svc := service.NewURLService(repo)
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

	expected := `{"code":"5BAN","short_url":"https://sho.rt/5BAN","expires_at":null}`

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

	svc := service.NewURLService(repo)
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

	svc := service.NewURLService(repo)
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

	svc := service.NewURLService(repo)
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

	svc := service.NewURLService(repo)
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

	svc := service.NewURLService(repo)
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

	svc := service.NewURLService(repo)
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
