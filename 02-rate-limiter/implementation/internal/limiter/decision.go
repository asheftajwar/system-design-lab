package limiter

type Decision struct {
	Allowed    bool
	Limit      int64
	Remaining  int64
	RetryAfter int64
	ResetAfter int64
}
