package domain

import "errors"

// ErrPostNotFound also covers posts inaccessible for an owner-only mutation.
var ErrPostNotFound = errors.New("post not found")
