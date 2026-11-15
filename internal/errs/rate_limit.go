package errs

// RateLimitError 保留服务端的重试时间，供嵌入宿主暂停整个账号。
type RateLimitError struct {
	RetryAfter string
}

func (e *RateLimitError) Error() string { return "HTTP 429: Too Many Requests" }
