package service

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/asheftajwar/system-design-lab/01-url-shortener/internal/analytics"
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

const maxURLLength = 2048

type URLService struct {
	repository        repository.URLRepository
	analyticsRepo     repository.AnalyticsRepository
	cache             cache.Cache
	analytics         analyticsEmitter
	baseURL           string
	metrics           Metrics
}

type analyticsEmitter interface {
	Emit(event analytics.RedirectEvent) bool
}

func (s *URLService) WithAnalyticsRepository(
	repo repository.AnalyticsRepository,
) *URLService {
	s.analyticsRepo = repo
	return s
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

func (s *URLService) WithMetrics(m Metrics) *URLService {
	s.metrics = m
	return s
}

func (s *URLService) WithAnalytics(emitter analyticsEmitter) *URLService {
	s.analytics = emitter
	return s
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

type URLMetadataOutput struct {
	Code           string
	OriginalURL    string
	CreatedAt      time.Time
	ExpiresAt      *time.Time
	RedirectCount  int64
	LastAccessedAt *time.Time
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

	// A custom alias may collide with a previously cached generated
	// code. Custom aliases take precedence, so invalidate that key.
	if entity.CustomAlias != nil && s.cache != nil {
		_ = s.cache.Delete(ctx, "url:"+*entity.CustomAlias)
	}

	if s.metrics != nil {
		s.metrics.Creation()
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

	if rawURL == "" || len(rawURL) > maxURLLength {
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
				if s.metrics != nil {
					s.metrics.CacheHit()
				}

				// Business expiration is checked independently
				// of Redis TTL.
				if isExpired(entry.ExpiresAt) {
					_ = s.cache.Delete(ctx, cacheKey)
					return nil, ErrURLExpired
				}

				s.recordRedirectAnalyticsEntity(entry.URLID)

				if s.metrics != nil {
					s.metrics.Redirect()
				}

				return &ResolveURLOutput{
					OriginalURL: entry.OriginalURL,
				}, nil
			}

			// Corrupt cache entry. Remove it and fall through
			// to PostgreSQL.
			_ = s.cache.Delete(ctx, cacheKey)

			if s.metrics != nil {
				s.metrics.CacheError()
			}
		} else {
			// Distinguish a normal cache miss from a Redis error.
			if errors.Is(err, cache.ErrNotFound) {
				if s.metrics != nil {
					s.metrics.CacheMiss()
				}
			} else {
				if s.metrics != nil {
					s.metrics.CacheError()
				}
			}
		}
	}

	// 2. Try custom alias.
	if s.metrics != nil {
		s.metrics.DBLookup()
	}

	urlEntity, err := s.repository.GetByAlias(ctx, code)

	if err == nil {
		if isExpired(urlEntity.ExpiresAt) {
			return nil, ErrURLExpired
		}

		s.cacheURL(ctx, cacheKey, urlEntity)
		s.recordRedirectAnalyticsEntity(urlEntity.ID)

		if s.metrics != nil {
			s.metrics.Redirect()
		}

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
	if s.metrics != nil {
		s.metrics.DBLookup()
	}

	urlEntity, err = s.repository.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if isExpired(urlEntity.ExpiresAt) {
		return nil, ErrURLExpired
	}

	// 5. Populate Redis for future requests.
	s.cacheURL(ctx, cacheKey, urlEntity)
	s.recordRedirectAnalyticsEntity(urlEntity.ID)

	if s.metrics != nil {
		s.metrics.Redirect()
	}

	return &ResolveURLOutput{
		OriginalURL: urlEntity.OriginalURL,
	}, nil
}

func (s *URLService) GetURLMetadata(
	ctx context.Context,
	code string,
) (*URLMetadataOutput, error) {
	// 1. Try custom alias first.
	urlEntity, err := s.repository.GetByAlias(ctx, code)

	if errors.Is(err, repository.ErrNotFound) {
		// 2. Not an alias. Try generated Base62 code.
		id, decodeErr := base62.Decode(code)
		if decodeErr != nil {
			return nil, repository.ErrNotFound
		}

		urlEntity, err = s.repository.GetByID(ctx, id)
	}

	if err != nil {
		return nil, err
	}

	output := &URLMetadataOutput{
		Code:           code,
		OriginalURL:    urlEntity.OriginalURL,
		CreatedAt:      urlEntity.CreatedAt,
		ExpiresAt:      urlEntity.ExpiresAt,
		RedirectCount:  0,
		LastAccessedAt: nil,
	}

	if s.analyticsRepo == nil {
		return output, nil
	}

	analyticsData, err := s.analyticsRepo.GetAnalytics(
		ctx,
		urlEntity.ID,
	)

	if errors.Is(err, repository.ErrNotFound) {
		return output, nil
	}

	if err != nil {
		return nil, err
	}

	output.RedirectCount = analyticsData.RedirectCount
	output.LastAccessedAt = analyticsData.LastAccessedAt

	return output, nil
}

func (s *URLService) recordRedirectAnalyticsEntity(urlID int64) {
	if s.analytics == nil {
		return
	}

	// Analytics emission is intentionally non-blocking.
	// A full analytics buffer must not delay a successful redirect.
	s.analytics.Emit(analytics.RedirectEvent{
		URLID:      urlID,
		AccessedAt: time.Now().UTC(),
	})
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
		URLID:      urlEntity.ID,
		OriginalURL: urlEntity.OriginalURL,
		ExpiresAt:   urlEntity.ExpiresAt,
	}

	value, err := cache.EncodeURL(entry)
	if err != nil {
		if s.metrics != nil {
			s.metrics.CacheError()
		}
		return
	}

	// Cache failures are intentionally ignored.
	// PostgreSQL remains the source of truth.
	if err := s.cache.Set(ctx, key, value); err != nil {
		if s.metrics != nil {
			s.metrics.CacheError()
		}
	}
}

func isExpired(expiresAt *time.Time) bool {
	return expiresAt != nil && !expiresAt.After(time.Now())
}