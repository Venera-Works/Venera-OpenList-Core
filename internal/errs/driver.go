package errs

import "errors"

var (
	EmptyToken   = errors.New("empty token")
	InvalidGrant = errors.New("invalid_grant")
)
