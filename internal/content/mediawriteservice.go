package content

import (
	"bytes"
	"context"
	"errors"
	"strings"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/sanitize"
)

// ErrUnsupportedMediaType is returned by MediaWriteService.Create when the
// upload's MIME type is outside the configured allow-list and the caller does
// not hold unfiltered_upload. It is a distinct sentinel from ErrForbidden so a
// transport can map it to 415 Unsupported Media Type rather than a generic
// 403/400 (design "Sentinels", Req 6.1).
var ErrUnsupportedMediaType = errors.New("content: unsupported media type")

// ErrUploadTooLarge is returned when an upload body exceeds the configured
// maximum size. It is a distinct sentinel so a transport can map it to 413
// Payload Too Large (design "Sentinels", Req 5).
var ErrUploadTooLarge = errors.New("content: upload too large")

// MediaWriteService performs capability-checked create and metadata-update of
// attachments. Create delegates binary storage to the embedded MediaService and
// gates on upload_files plus the MIME allow-list; Update loads the stored
// attachment, authorizes against its post_author, sanitizes each supplied
// metadata field at the actor's tier, and persists through the writer port.
type MediaWriteService struct {
	media  *MediaService
	reader domain.MediaRepository
	writer domain.MediaWriter
	// policy is the write-boundary content policy applied to the caller's
	// metadata fields on Update (Req 4). It defaults to sanitize.New() in
	// NewMediaWriteService and is NEVER nil: an unwired service sanitizes
	// rather than silently passing input through, so the fail-closed guarantee
	// holds even for a call site that forgets WithMediaContentPolicy, matching
	// PostWriteService/CommentService/Server.
	policy *sanitize.Policy
}

// MediaWriteOption configures optional MediaWriteService dependencies, mirroring
// the PostWriteOption style.
type MediaWriteOption func(*MediaWriteService)

// WithMediaContentPolicy wires the write-boundary content policy (in
// production, the process-wide sanitize.New()) into MediaWriteService so Update
// sanitizes the caller's metadata fields at the actor's tier (Req 4, 11.1).
// When omitted, NewMediaWriteService's sanitize.New() default still applies, so
// the service never silently skips sanitization (fail closed).
func WithMediaContentPolicy(p *sanitize.Policy) MediaWriteOption {
	return func(s *MediaWriteService) { s.policy = p }
}

// NewMediaWriteService constructs a MediaWriteService over a MediaService (for
// binary storage), a MediaRepository reader (for loading/re-reading stored
// attachments), and a MediaWriter (for metadata/parent persistence). The
// content policy defaults to sanitize.New() (never nil / fail closed); pass
// WithMediaContentPolicy to share the process-wide policy.
func NewMediaWriteService(media *MediaService, reader domain.MediaRepository, writer domain.MediaWriter, opts ...MediaWriteOption) *MediaWriteService {
	s := &MediaWriteService{media: media, reader: reader, writer: writer, policy: sanitize.New()}
	for _, opt := range opts {
		opt(s)
	}
	if s.policy == nil {
		s.policy = sanitize.New()
	}
	return s
}

// MediaCreateInput is the binary-upload payload for Create.
type MediaCreateInput struct {
	Data     []byte
	Filename string
	MimeType string
}

// MediaMetaUpdate is a partial metadata update: each pointer field is applied
// only when non-nil (nil = omitted / leave the stored value unchanged). Parent
// is included here but routed through MediaWriter.SetParent, not MediaMeta
// (Req 2.2, 3.5).
type MediaMetaUpdate struct {
	Title       *string
	Caption     *string
	AltText     *string
	Description *string
	Parent      *int64
}

// Create authorizes and stores a new attachment. It requires upload_files and,
// unless the caller holds unfiltered_upload, enforces the MIME allow-list
// before any storage work. The acting principal's UserID is threaded into the
// created attachment's post_author (Req 1.4). No sanitization runs here: a
// create carries only binary data (Req 1.3).
func (s *MediaWriteService) Create(ctx context.Context, actor auth.Principal, in MediaCreateInput) (domain.Media, error) {
	if !actor.Can("upload_files") {
		return domain.Media{}, ErrForbidden
	}
	if !actor.Can("unfiltered_upload") && !mimeAllowedForMedia(in.MimeType, s.media.Config().AllowedMIMEs) {
		return domain.Media{}, ErrUnsupportedMediaType
	}
	return s.media.Store(ctx, bytes.NewReader(in.Data), MediaUpload{
		Filename: in.Filename,
		MimeType: in.MimeType,
		Author:   actor.UserID,
	})
}

// Update authorizes and applies a partial metadata update to an existing
// attachment. It loads the stored row by ID (bubbling ErrNotFound, Req 2.5),
// authorizes against the stored post_author (Req 5.3-5.5), sanitizes each
// supplied field at its mapped kind and the actor's tier (failing closed on any
// policy error, Req 4.7), persists metadata via UpdateMeta and any supplied
// parent via SetParent (Req 2.2), then re-reads the stored row so the returned
// Media reflects persisted values (Req 10.3).
func (s *MediaWriteService) Update(ctx context.Context, actor auth.Principal, id int64, upd MediaMetaUpdate) (domain.Media, error) {
	cur, err := s.reader.ByID(ctx, id)
	if err != nil {
		return domain.Media{}, err
	}
	// Ownership keys off the STORED post_author. post_type "post" selects the
	// *_posts capability family; status "inherit" keeps the published/private
	// branches from firing for an attachment.
	if !auth.CanEditPost(actor, "post", "inherit", cur.AuthorID) {
		return domain.Media{}, ErrForbidden
	}

	writer := sanitize.For(actor)
	meta := domain.MediaMeta{
		Title:       cur.Title,
		Caption:     cur.Caption,
		AltText:     cur.AltText,
		Description: cur.Description,
	}

	if upd.Title != nil {
		cleaned, err := s.policy.Sanitize(sanitize.PostTitle, writer, *upd.Title)
		if err != nil {
			return domain.Media{}, err
		}
		meta.Title = cleaned
	}
	if upd.Caption != nil {
		cleaned, err := s.policy.Sanitize(sanitize.PostExcerpt, writer, *upd.Caption)
		if err != nil {
			return domain.Media{}, err
		}
		meta.Caption = cleaned
	}
	if upd.AltText != nil {
		cleaned, err := s.policy.Sanitize(sanitize.PostTitle, writer, *upd.AltText)
		if err != nil {
			return domain.Media{}, err
		}
		meta.AltText = cleaned
	}
	if upd.Description != nil {
		cleaned, err := s.policy.Sanitize(sanitize.PostContent, writer, *upd.Description)
		if err != nil {
			return domain.Media{}, err
		}
		meta.Description = cleaned
	}

	if err := s.writer.UpdateMeta(ctx, id, meta); err != nil {
		return domain.Media{}, err
	}
	if upd.Parent != nil {
		if err := s.writer.SetParent(ctx, id, *upd.Parent); err != nil {
			return domain.Media{}, err
		}
	}
	return s.reader.ByID(ctx, id)
}

// mimeAllowedForMedia reports whether mimeType is permitted by allowed. An empty
// allow-list means "no restriction configured" -- always allow. Comparison is
// on the base MIME, stripping any "; charset=..." (or other) parameter suffix.
// This is package content's own predicate; the web package has an equivalent
// (mimeAllowed/baseMime) that content must not import.
func mimeAllowedForMedia(mimeType string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	base := baseMimeForMedia(mimeType)
	for _, a := range allowed {
		if strings.EqualFold(base, baseMimeForMedia(a)) {
			return true
		}
	}
	return false
}

// baseMimeForMedia strips any parameter suffix from a MIME type string.
func baseMimeForMedia(s string) string {
	if i := strings.IndexByte(s, ';'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
