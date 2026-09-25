package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/asheftajwar/system-design-lab/01-url-shortener/internal/base62"
	"github.com/asheftajwar/system-design-lab/01-url-shortener/internal/cache"
	"github.com/asheftajwar/system-design-lab/01-url-shortener/internal/domain"
	"github.com/asheftajwar/system-design-lab/01-url-shortener/internal/repository"
)

type fakeURLRepository struct {
	createFunc     func(ctx context.Context, url *domain.URL) error
	getByIDFunc    func(ctx context.Context, id int64) (*domain.URL, error)
	getByAliasFunc func(ctx context.Context, alias string) (*domain.URL, error)
}

type fakeCache struct {
	values     map[string]string
	getErr     error
	setErr     error
	deleteErr  error
	getCalls   int
	setCalls   int
	getFunc    func(ctx context.Context, key string) (string, error)
	setFunc    func(ctx context.Context, key string, value string) error
	deleteFunc func(ctx context.Context, key string) error
}

func (f *fakeCache) Delete(ctx context.Context, key string) error {
	if f.deleteFunc != nil {
		return f.deleteFunc(ctx, key)
	}

	if f.deleteErr != nil {
		return f.deleteErr
	}

	return nil
}

func newFakeCache() *fakeCache {
	return &fakeCache{
		values: make(map[string]string),
	}
}

func (c *fakeCache) Get(ctx context.Context, key string) (string, error) {
	c.getCalls++

	if c.getFunc != nil {
		return c.getFunc(ctx, key)
	}

	if c.getErr != nil {
		return "", c.getErr
	}

	value, ok := c.values[key]
	if !ok {
		return "", cache.ErrNotFound
	}

	return value, nil
}

func (f *fakeCache) Set(
	ctx context.Context,
	key string,
	value string,
) error {
	if f.setFunc != nil {
		return f.setFunc(ctx, key, value)
	}

	if f.setErr != nil {
		return f.setErr
	}

	if f.values == nil {
		f.values = make(map[string]string)
	}

	f.values[key] = value
	f.setCalls++

	return nil
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

func TestURLServiceResolveURLCacheHit(t *testing.T) {
	repo := &fakeURLRepository{
		createFunc: func(ctx context.Context, url *domain.URL) error {
			url.ID = 1234567
			url.CreatedAt = time.Now()
			return nil
		},
	}
	urlCache := newFakeCache()

	cachedURL := "https://example.com/from-cache"

	entry := cache.URLCacheEntry{
		OriginalURL: cachedURL,
	}

	value, err := cache.EncodeURL(entry)
	if err != nil {
		t.Fatalf("EncodeURL() error = %v", err)
	}

	urlCache.values["url:abc"] = value

	svc := NewURLService(repo, urlCache)

	result, err := svc.ResolveURL(context.Background(), "abc")
	if err != nil {
		t.Fatalf("ResolveURL() error = %v", err)
	}

	if result.OriginalURL != cachedURL {
		t.Fatalf(
			"OriginalURL = %q, want %q",
			result.OriginalURL,
			cachedURL,
		)
	}

	if urlCache.getCalls != 1 {
		t.Fatalf(
			"cache Get() calls = %d, want 1",
			urlCache.getCalls,
		)
	}
}

func TestURLServiceResolveURLCacheMiss(t *testing.T) {
	urlCache := newFakeCache()

	repo := &fakeURLRepository{
		getByAliasFunc: func(ctx context.Context, alias string) (*domain.URL, error) {
			return nil, repository.ErrNotFound
		},
		getByIDFunc: func(ctx context.Context, id int64) (*domain.URL, error) {
			if id != 123 {
				t.Fatalf("expected ID 123, got %d", id)
			}

			return &domain.URL{
				ID:          123,
				OriginalURL: "https://example.com/from-database",
				CreatedAt:   time.Now(),
			}, nil
		},
	}

	svc := NewURLService(repo, urlCache)

	code := base62.Encode(123)

	result, err := svc.ResolveURL(context.Background(), code)
	if err != nil {
		t.Fatalf("ResolveURL() error = %v", err)
	}

	if result.OriginalURL != "https://example.com/from-database" {
		t.Fatalf(
			"OriginalURL = %q, want %q",
			result.OriginalURL,
			"https://example.com/from-database",
		)
	}

	if urlCache.setCalls != 1 {
		t.Fatalf(
			"cache Set() calls = %d, want 1",
			urlCache.setCalls,
		)
	}

	cachedValue, ok := urlCache.values["url:"+code]
	if !ok {
		t.Fatal("expected URL to be stored in cache")
	}

	entry, err := cache.DecodeURL(cachedValue)
	if err != nil {
		t.Fatalf("DecodeURL() error = %v", err)
	}

	if entry.OriginalURL != "https://example.com/from-database" {
		t.Fatalf(
			"cached OriginalURL = %q, want %q",
			entry.OriginalURL,
			"https://example.com/from-database",
		)
	}
}

func TestURLServiceResolveURLExpiredCacheEntry(t *testing.T) {
	expiredAt := time.Now().Add(-1 * time.Hour)

	entry := cache.URLCacheEntry{
		OriginalURL: "https://example.com/expired",
		ExpiresAt:   &expiredAt,
	}

	value, err := cache.EncodeURL(entry)
	if err != nil {
		t.Fatalf("EncodeURL() error = %v", err)
	}

	deleted := false

	urlCache := &fakeCache{
		values: map[string]string{
			"url:expired": value,
		},
	}

	urlCache.deleteFunc = func(ctx context.Context, key string) error {
		deleted = true
		delete(urlCache.values, key)
		return nil
	}

	repo := &fakeURLRepository{}

	svc := NewURLService(repo, urlCache)

	_, err = svc.ResolveURL(context.Background(), "expired")

	if !errors.Is(err, ErrURLExpired) {
		t.Fatalf(
			"ResolveURL() error = %v, want %v",
			err,
			ErrURLExpired,
		)
	}

	if !deleted {
		t.Fatal("expected expired cache entry to be deleted")
	}

	if _, exists := urlCache.values["url:expired"]; exists {
		t.Fatal("expected expired cache entry to be removed from cache")
	}
}

func TestURLServiceResolveURLCacheFailureFallsBackToRepository(t *testing.T) {
	urlCache := newFakeCache()
	urlCache.getErr = errors.New("redis unavailable")

	repo := &fakeURLRepository{
		getByAliasFunc: func(ctx context.Context, alias string) (*domain.URL, error) {
			return nil, repository.ErrNotFound
		},
		getByIDFunc: func(ctx context.Context, id int64) (*domain.URL, error) {
			if id != 456 {
				t.Fatalf("expected ID 456, got %d", id)
			}

			return &domain.URL{
				ID:          456,
				OriginalURL: "https://example.com/database-fallback",
				CreatedAt:   time.Now(),
			}, nil
		},
	}

	svc := NewURLService(repo, urlCache)

	code := base62.Encode(456)

	result, err := svc.ResolveURL(context.Background(), code)
	if err != nil {
		t.Fatalf("ResolveURL() error = %v", err)
	}

	if result.OriginalURL != "https://example.com/database-fallback" {
		t.Fatalf(
			"OriginalURL = %q, want %q",
			result.OriginalURL,
			"https://example.com/database-fallback",
		)
	}

	if urlCache.getCalls != 1 {
		t.Fatalf(
			"cache Get() calls = %d, want 1",
			urlCache.getCalls,
		)
	}
}

func TestURLServiceResolveURLCorruptCacheFallsBackToRepository(t *testing.T) {
	repo := &fakeURLRepository{
		getByAliasFunc: func(ctx context.Context, alias string) (*domain.URL, error) {
			return nil, repository.ErrNotFound
		},
		getByIDFunc: func(ctx context.Context, id int64) (*domain.URL, error) {
			return &domain.URL{
				ID:          id,
				OriginalURL: "https://example.com/from-postgres",
				CreatedAt:   time.Now(),
			}, nil
		},
	}

	deleted := false

	fakeCache := &fakeCache{
		getFunc: func(ctx context.Context, key string) (string, error) {
			return "this is not valid JSON", nil
		},
		deleteFunc: func(ctx context.Context, key string) error {
			deleted = true
			return nil
		},
	}

	svc := NewURLService(repo, fakeCache)

	code := base62.Encode(1234567)

	result, err := svc.ResolveURL(context.Background(), code)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if result == nil {
		t.Fatal("expected result, got nil")
	}

	if result.OriginalURL != "https://example.com/from-postgres" {
		t.Fatalf(
			"expected URL from repository, got %q",
			result.OriginalURL,
		)
	}

	if !deleted {
		t.Fatal("expected corrupt cache entry to be deleted")
	}
}

func TestURLServiceCreateURLDuplicateAlias(t *testing.T) {
	repo := &fakeURLRepository{
		createFunc: func(ctx context.Context, url *domain.URL) error {
			return repository.ErrDuplicateAlias
		},
	}

	svc := NewURLService(repo)

	alias := "docs"

	_, err := svc.CreateURL(
		context.Background(),
		CreateURLInput{
			OriginalURL: "https://example.com/docs",
			CustomAlias: &alias,
		},
	)

	if !errors.Is(err, repository.ErrDuplicateAlias) {
		t.Fatalf(
			"CreateURL() error = %v, want %v",
			err,
			repository.ErrDuplicateAlias,
		)
	}
}