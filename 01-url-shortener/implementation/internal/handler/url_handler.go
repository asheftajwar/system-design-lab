package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/asheftajwar/system-design-lab/01-url-shortener/internal/repository"
	"github.com/asheftajwar/system-design-lab/01-url-shortener/internal/service"
)

const maxCreateURLBodySize = 16 << 10 // 16 KiB

type URLHandler struct {
	service *service.URLService
}

func NewURLHandler(svc *service.URLService) *URLHandler {
	return &URLHandler{
		service: svc,
	}
}

type createURLRequest struct {
	URL         string     `json:"url"`
	ExpiresAt   *time.Time `json:"expires_at"`
	CustomAlias *string    `json:"custom_alias"`
}

type createURLResponse struct {
	Code      string     `json:"code"`
	ShortURL  string     `json:"short_url"`
	ExpiresAt *time.Time `json:"expires_at"`
}

type urlMetadataResponse struct {
	Code           string     `json:"code"`
	OriginalURL    string     `json:"original_url"`
	CreatedAt      time.Time  `json:"created_at"`
	ExpiresAt      *time.Time `json:"expires_at"`
	RedirectCount  int64      `json:"redirect_count"`
	LastAccessedAt *time.Time `json:"last_accessed_at"`
}

func (h *URLHandler) CreateURL(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxCreateURLBodySize)

	decoder := json.NewDecoder(r.Body)

	var req createURLRequest

	if err := decoder.Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if decoder.Decode(&struct{}{}) != io.EOF {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	result, err := h.service.CreateURL(r.Context(), service.CreateURLInput{
		OriginalURL: req.URL,
		CustomAlias: req.CustomAlias,
		ExpiresAt:   req.ExpiresAt,
	})
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidURL),
			errors.Is(err, service.ErrInvalidAlias),
			errors.Is(err, service.ErrExpirationPast):
			writeJSONError(w, http.StatusBadRequest, err.Error())

		case errors.Is(err, repository.ErrDuplicateAlias):
			writeJSONError(w, http.StatusConflict, "custom alias already exists")

		default:
			writeJSONError(w, http.StatusInternalServerError, "internal server error")
		}

		return
	}

	response := createURLResponse{
		Code:      result.Code,
		ShortURL:  result.ShortURL,
		ExpiresAt: result.ExpiresAt,
	}

	writeJSON(w, http.StatusCreated, response)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(value)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{
		"error": message,
	})
}

func (h *URLHandler) Redirect(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")

	if code == "" {
		http.NotFound(w, r)
		return
	}

	result, err := h.service.ResolveURL(r.Context(), code)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrURLExpired):
			writeJSONError(w, http.StatusGone, "url expired")

		case errors.Is(err, repository.ErrNotFound):
			http.NotFound(w, r)

		default:
			writeJSONError(
				w,
				http.StatusInternalServerError,
				"internal server error",
			)
		}

		return
	}

	http.Redirect(
		w,
		r,
		result.OriginalURL,
		http.StatusFound,
	)
}

func (h *URLHandler) GetMetadata(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")

	if code == "" {
		http.NotFound(w, r)
		return
	}

	result, err := h.service.GetURLMetadata(r.Context(), code)
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrNotFound):
			http.NotFound(w, r)

		default:
			writeJSONError(
				w,
				http.StatusInternalServerError,
				"internal server error",
			)
		}

		return
	}

	response := urlMetadataResponse{
		Code:           result.Code,
		OriginalURL:    result.OriginalURL,
		CreatedAt:      result.CreatedAt,
		ExpiresAt:      result.ExpiresAt,
		RedirectCount:  result.RedirectCount,
		LastAccessedAt: result.LastAccessedAt,
	}

	writeJSON(w, http.StatusOK, response)
}
