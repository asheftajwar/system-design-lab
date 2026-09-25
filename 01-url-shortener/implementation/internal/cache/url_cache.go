package cache

import (
	"encoding/json"
	"time"
)

type URLCacheEntry struct {
	OriginalURL string     `json:"original_url"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

func EncodeURL(entry URLCacheEntry) (string, error) {
	data, err := json.Marshal(entry)
	if err != nil {
		return "", err
	}

	return string(data), nil
}

func DecodeURL(value string) (URLCacheEntry, error) {
	var entry URLCacheEntry

	if err := json.Unmarshal([]byte(value), &entry); err != nil {
		return URLCacheEntry{}, err
	}

	return entry, nil
}
