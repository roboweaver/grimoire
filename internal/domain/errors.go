package domain

import "errors"

// ErrNotFound is returned by repositories when a requested record does not
// exist. Callers should test for it with errors.Is.
var ErrNotFound = errors.New("grimoire: resource not found")
