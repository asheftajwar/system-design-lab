package service

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/asheftajwar/system-design-lab/01-url-shortener/internal/base62"
	"github.com/asheftajwar/system-design-lab/01-url-shortener/internal/cache"
	"github.com/asheftajwar/system-design-lab/01-url-shortener/internal/domain"
	"github.com/asheftajwar/system-design-lab/01-url-shortener/internal/repository"
)

var (
	ErrInvalidURL     = errors.New("invalid URL")
	ErrInvalidAlias   = errors.New("invalid custom alias")
	ErrExpirationPast = errors.New("expiration time must be in the future")
	ErrURLExpired     = errors.New("url expired")
)

type URLService struct {
	repository repository.URLRepository
	cache      cache.Cache
	baseURL    string
}

func NewURLService(
	repo repository.URLRepository,
	baseURL string,
	caches ...cache.Cache,
) *URLService {
	var urlCache cache.Cache

	if len(caches) > 0 {
		urlCache = caches[0]
	}

	return &URLService{
		repository: repo,
		cache:      urlCache,
		baseURL:    strings.TrimRight(baseURL, "/"),
	}
}

type CreateURLInput struct {
	OriginalURL string
	CustomAlias *string
	ExpiresAt   *time.Time
}

type CreateURLOutput struct {
	Code      string
	ShortURL  string
	ExpiresAt *time.Time
}

type ResolveURLOutput struct {
	OriginalURL string
}

func (s *URLService) CreateURL(
	ctx context.Context,
	input CreateURLInput,
) (*CreateURLOutput, error) {
	if err := validateURL(input.OriginalURL); err != nil {
		return nil, err
	}

	if err := validateExpiration(input.ExpiresAt); err != nil {
		return nil, err
	}

	if err := validateAlias(input.CustomAlias); err != nil {
		return nil, err
	}

	entity := &domain.URL{
		OriginalURL: input.OriginalURL,
		CustomAlias: input.CustomAlias,
		ExpiresAt:   input.ExpiresAt,
	}

	if err := s.repository.Create(ctx, entity); err != nil {
		return nil, err
	}

	code := base62.Encode(entity.ID)

	if entity.CustomAlias != nil {
		code = *entity.CustomAlias
	}

	return &CreateURLOutput{
		Code:      code,
		ShortURL:  s.baseURL + "/" + code,
		ExpiresAt: entity.ExpiresAt,
	}, nil
}

func validateURL(rawURL string) error {
	rawURL = strings.TrimSpace(rawURL)

	if rawURL == "" {
		return ErrInvalidURL
	}

	parsed, err := url.ParseRequestURI(rawURL)
	if err != nil {
		return ErrInvalidURL
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ErrInvalidURL
	}

	if parsed.Host == "" {
		return ErrInvalidURL
	}

	return nil
}

func validateExpiration(expiresAt *time.Time) error {
	if expiresAt == nil {
		return nil
	}

	if !expiresAt.After(time.Now()) {
		return ErrExpirationPast
	}

	return nil
}

func validateAlias(alias *string) error {
	if alias == nil {
		return nil
	}

	value := strings.TrimSpace(*alias)

	if value == "" || len(value) > 64 {
		return ErrInvalidAlias
	}

	for _, char := range value {
		if !isAliasCharacter(char) {
			return ErrInvalidAlias
		}
	}

	return nil
}

func isAliasCharacter(char rune) bool {
	return (char >= 'a' && char <= 'z') ||
		(char >= 'A' && char <= 'Z') ||
		(char >= '0' && char <= '9') ||
		char == '-' ||
		char == '_'
}

func (s *URLService) ResolveURL(
	ctx context.Context,
	code string,
) (*ResolveURLOutput, error) {
	cacheKey := "url:" + code

	// 1. Try Redis first.
	if s.cache != nil {
		cachedValue, err := s.cache.Get(ctx, cacheKey)

		if err == nil {
			entry, decodeErr := cache.DecodeURL(cachedValue)

			if decodeErr == nil {
				// Business expiration is checked independently
				// of Redis TTL.
				if isExpired(entry.ExpiresAt) {
					_ = s.cache.Delete(ctx, cacheKey)
					return nil, ErrURLExpired
				}

				return &ResolveURLOutput{
					OriginalURL: entry.OriginalURL,
				}, nil
			}

			// Corrupt cache entry. Remove it and fall
			// through to PostgreSQL.
			_ = s.cache.Delete(ctx, cacheKey)
		}

		// Redis failure or cache miss should not break
		// redirects. Fall through to PostgreSQL.
	}

	// 2. Try custom alias.
	urlEntity, err := s.repository.GetByAlias(ctx, code)

	if err == nil {
		if isExpired(urlEntity.ExpiresAt) {
			return nil, ErrURLExpired
		}

		s.cacheURL(ctx, cacheKey, urlEntity)

		return &ResolveURLOutput{
			OriginalURL: urlEntity.OriginalURL,
		}, nil
	}

	if !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}

	// 3. Decode generated Base62 code.
	id, err := base62.Decode(code)
	if err != nil {
		return nil, repository.ErrNotFound
	}

	// 4. Look up generated URL by ID.
	urlEntity, err = s.repository.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if isExpired(urlEntity.ExpiresAt) {
		return nil, ErrURLExpired
	}

	// 5. Populate Redis for future requests.
	s.cacheURL(ctx, cacheKey, urlEntity)

	return &ResolveURLOutput{
		OriginalURL: urlEntity.OriginalURL,
	}, nil
}

func (s *URLService) cacheURL(
	ctx context.Context,
	key string,
	urlEntity *domain.URL,
) {
	if s.cache == nil {
		return
	}

	entry := cache.URLCacheEntry{
		OriginalURL: urlEntity.OriginalURL,
		ExpiresAt:   urlEntity.ExpiresAt,
	}

	value, err := cache.EncodeURL(entry)
	if err != nil {
		return
	}

	// Cache failures are intentionally ignored.
	// PostgreSQL remains the source of truth.
	_ = s.cache.Set(ctx, key, value)
}

func isExpired(expiresAt *time.Time) bool {
	return expiresAt != nil && !expiresAt.After(time.Now())
}
