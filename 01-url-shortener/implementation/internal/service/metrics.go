package service

type Metrics interface {
	CacheHit()
	CacheMiss()
	CacheError()
	DBLookup()
	Redirect()
	Creation()
}
