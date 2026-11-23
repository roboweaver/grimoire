package domain

import "errors"

// ErrNotFound is returned by repositories when a requested record does not
// exist. Callers should test for it with errors.Is.
var ErrNotFound = errors.New("grimoire: resource not found")

// ErrAlreadyTrashed is returned by PostTrasher.TrashPost when the row is
// already in the trash status. It is distinct from ErrNotFound.
var ErrAlreadyTrashed = errors.New("grimoire: post is already trashed")

// ErrNotTrashed is returned by PostTrasher.RestorePost when the row is not in
// the trash status. It is distinct from ErrNotFound.
var ErrNotTrashed = errors.New("grimoire: post is not trashed")
