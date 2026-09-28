CREATE TABLE url_analytics (
    url_id BIGINT PRIMARY KEY REFERENCES urls(id) ON DELETE CASCADE,
    redirect_count BIGINT NOT NULL DEFAULT 0,
    last_accessed_at TIMESTAMPTZ
);