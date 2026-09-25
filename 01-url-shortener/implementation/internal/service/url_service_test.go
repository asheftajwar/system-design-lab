package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/asheftajwar/system-design-lab/01-url-shortener/internal/domain"
	"github.com/asheftajwar/system-design-lab/01-url-shortener/internal/repository"
)

type fakeURLRepository struct {
	createFunc     func(ctx context.Context, url *domain.URL) error
	getByIDFunc    func(ctx context.Context, id int64) (*domain.URL, error)
	getByAliasFunc func(ctx context.Context, alias string) (*domain.URL, error)
}

func (f *fakeURLRepository) Create(
	ctx context.Context,
	url *domain.URL,
) error {
	if f.createFunc != nil {
		return f.createFunc(ctx, url)
	}

	return nil
}

func (f *fakeURLRepository) GetByID(
	ctx context.Context,
	id int64,
) (*domain.URL, error) {
	if f.getByIDFunc != nil {
		return f.getByIDFunc(ctx, id)
	}

	return nil, nil
}

func (f *fakeURLRepository) GetByAlias(
	ctx context.Context,
	alias string,
) (*domain.URL, error) {
	if f.getByAliasFunc != nil {
		return f.getByAliasFunc(ctx, alias)
	}

	return nil, nil
}

func TestURLServiceCreateURL(t *testing.T) {
	repo := &fakeURLRepository{
		createFunc: func(ctx context.Context, url *domain.URL) error {
			url.ID = 1234567
			url.CreatedAt = time.Now()
			return nil
		},
	}

	service := NewURLService(repo)

	result, err := service.CreateURL(
		context.Background(),
		CreateURLInput{
			OriginalURL: "https://example.com/very/long/path",
		},
	)

	if err != nil {
		t.Fatalf("CreateURL returned error: %v", err)
	}

	if result.Code != "5BAN" {
		t.Fatalf("expected code 5BAN, got %q", result.Code)
	}

	if result.ShortURL != "https://sho.rt/5BAN" {
		t.Fatalf(
			"expected short URL https://sho.rt/5BAN, got %q",
			result.ShortURL,
		)
	}

	if result.ExpiresAt != nil {
		t.Fatal("expected no expiration")
	}
}

func TestURLServiceCreateURLWithCustomAlias(t *testing.T) {
	repo := &fakeURLRepository{
		createFunc: func(ctx context.Context, url *domain.URL) error {
			url.ID = 1234567
			url.CreatedAt = time.Now()
			return nil
		},
	}

	service := NewURLService(repo)

	alias := "docs"

	result, err := service.CreateURL(
		context.Background(),
		CreateURLInput{
			OriginalURL: "https://example.com/docs",
			CustomAlias: &alias,
		},
	)

	if err != nil {
		t.Fatalf("CreateURL returned error: %v", err)
	}

	if result.Code != "docs" {
		t.Fatalf("expected code docs, got %q", result.Code)
	}

	if result.ShortURL != "https://sho.rt/docs" {
		t.Fatalf(
			"expected short URL https://sho.rt/docs, got %q",
			result.ShortURL,
		)
	}
}

func TestURLServiceCreateURLWithExpiration(t *testing.T) {
	repo := &fakeURLRepository{
		createFunc: func(ctx context.Context, url *domain.URL) error {
			url.ID = 123
			url.CreatedAt = time.Now()
			return nil
		},
	}

	service := NewURLService(repo)

	expiresAt := time.Now().Add(time.Hour)

	result, err := service.CreateURL(
		context.Background(),
		CreateURLInput{
			OriginalURL: "https://example.com",
			ExpiresAt:   &expiresAt,
		},
	)

	if err != nil {
		t.Fatalf("CreateURL returned error: %v", err)
	}

	if result.ExpiresAt == nil {
		t.Fatal("expected expiration time")
	}

	if !result.ExpiresAt.Equal(expiresAt) {
		t.Fatalf(
			"expected expiration %v, got %v",
			expiresAt,
			*result.ExpiresAt,
		)
	}
}

func TestURLServiceRejectsInvalidURL(t *testing.T) {
	service := NewURLService(&fakeURLRepository{})

	tests := []struct {
		name string
		url  string
	}{
		{
			name: "empty URL",
			url:  "",
		},
		{
			name: "missing scheme",
			url:  "example.com",
		},
		{
			name: "unsupported scheme",
			url:  "ftp://example.com/file",
		},
		{
			name: "missing host",
			url:  "https:///path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := service.CreateURL(
				context.Background(),
				CreateURLInput{
					OriginalURL: tt.url,
				},
			)

			if !errors.Is(err, ErrInvalidURL) {
				t.Fatalf(
					"expected ErrInvalidURL, got %v",
					err,
				)
			}
		})
	}
}

func TestURLServiceRejectsPastExpiration(t *testing.T) {
	service := NewURLService(&fakeURLRepository{})

	expiresAt := time.Now().Add(-time.Hour)

	_, err := service.CreateURL(
		context.Background(),
		CreateURLInput{
			OriginalURL: "https://example.com",
			ExpiresAt:   &expiresAt,
		},
	)

	if !errors.Is(err, ErrExpirationPast) {
		t.Fatalf(
			"expected ErrExpirationPast, got %v",
			err,
		)
	}
}

func TestURLServiceRejectsInvalidAlias(t *testing.T) {
	service := NewURLService(&fakeURLRepository{})

	tests := []struct {
		name  string
		alias string
	}{
		{
			name:  "empty alias",
			alias: "",
		},
		{
			name:  "alias with spaces",
			alias: "my docs",
		},
		{
			name:  "alias with slash",
			alias: "docs/test",
		},
		{
			name:  "alias with special character",
			alias: "docs!",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			alias := tt.alias

			_, err := service.CreateURL(
				context.Background(),
				CreateURLInput{
					OriginalURL: "https://example.com",
					CustomAlias: &alias,
				},
			)

			if !errors.Is(err, ErrInvalidAlias) {
				t.Fatalf(
					"expected ErrInvalidAlias, got %v",
					err,
				)
			}
		})
	}
}

func TestURLServicePropagatesRepositoryError(t *testing.T) {
	expectedErr := errors.New("database unavailable")

	repo := &fakeURLRepository{
		createFunc: func(ctx context.Context, url *domain.URL) error {
			return expectedErr
		},
	}

	service := NewURLService(repo)

	_, err := service.CreateURL(
		context.Background(),
		CreateURLInput{
			OriginalURL: "https://example.com",
		},
	)

	if !errors.Is(err, expectedErr) {
		t.Fatalf(
			"expected repository error %v, got %v",
			expectedErr,
			err,
		)
	}
}

func TestURLServiceResolveURLByGeneratedCode(t *testing.T) {
	repo := &fakeURLRepository{
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
			if id != 1234567 {
				t.Fatalf("expected ID 1234567, got %d", id)
			}

			return &domain.URL{
				ID:          id,
				OriginalURL: "https://example.com/generated",
				CreatedAt:   time.Now(),
			}, nil
		},
	}

	svc := NewURLService(repo)

	result, err := svc.ResolveURL(
		context.Background(),
		"5BAN",
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if result.OriginalURL != "https://example.com/generated" {
		t.Fatalf(
			"expected original URL %q, got %q",
			"https://example.com/generated",
			result.OriginalURL,
		)
	}
}

func TestURLServiceResolveURLByCustomAlias(t *testing.T) {
	repo := &fakeURLRepository{
		getByAliasFunc: func(
			ctx context.Context,
			alias string,
		) (*domain.URL, error) {
			if alias != "docs" {
				t.Fatalf("expected alias docs, got %s", alias)
			}

			return &domain.URL{
				ID:          123,
				OriginalURL: "https://example.com/docs",
				CustomAlias: &alias,
				CreatedAt:   time.Now(),
			}, nil
		},
	}

	svc := NewURLService(repo)

	result, err := svc.ResolveURL(
		context.Background(),
		"docs",
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if result.OriginalURL != "https://example.com/docs" {
		t.Fatalf(
			"expected original URL %q, got %q",
			"https://example.com/docs",
			result.OriginalURL,
		)
	}
}

func TestURLServiceResolveURLExpired(t *testing.T) {
	expiredAt := time.Now().Add(-time.Hour)

	repo := &fakeURLRepository{
		getByAliasFunc: func(
			ctx context.Context,
			alias string,
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

	svc := NewURLService(repo)

	_, err := svc.ResolveURL(
		context.Background(),
		"expired",
	)

	if !errors.Is(err, ErrURLExpired) {
		t.Fatalf(
			"expected ErrURLExpired, got %v",
			err,
		)
	}
}

func TestURLServiceResolveURLNotFound(t *testing.T) {
	repo := &fakeURLRepository{
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

	svc := NewURLService(repo)

	_, err := svc.ResolveURL(
		context.Background(),
		"does-not-exist",
	)

	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf(
			"expected repository.ErrNotFound, got %v",
			err,
		)
	}
}

func TestURLServiceResolveURLAliasTakesPrecedence(t *testing.T) {
	alias := "5BAN"

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
				ID:          999,
				OriginalURL: "https://example.com/custom",
				CustomAlias: &alias,
				CreatedAt:   time.Now(),
			}, nil
		},
		getByIDFunc: func(
			ctx context.Context,
			id int64,
		) (*domain.URL, error) {
			t.Fatal("GetByID should not be called when alias exists")

			return nil, nil
		},
	}

	svc := NewURLService(repo)

	result, err := svc.ResolveURL(
		context.Background(),
		"5BAN",
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if result.OriginalURL != "https://example.com/custom" {
		t.Fatalf(
			"expected custom alias URL, got %q",
			result.OriginalURL,
		)
	}
}
