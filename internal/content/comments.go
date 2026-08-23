package content

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/roboweaver/grimoire/internal/domain"
)

const (
	commentStatusHold  = "0"
	commentStatusOK    = "1"
	commentStatusSpam  = "spam"
	commentStatusTrash = "trash"

	spamVerdictApprove = "approve"
	spamVerdictHold    = "hold"
	spamVerdictSpam    = "spam"

	wpTrashMetaStatus = "_wp_trash_meta_status"
	wpTrashMetaTime   = "_wp_trash_meta_time"
)

var ErrCommentsClosed = errors.New("content: comments closed")

type commentPostReader interface {
	ByID(ctx context.Context, id int64) (domain.Post, error)
}

type CommentService struct {
	repo   domain.CommentRepository
	writer domain.CommentWriter
	meta   domain.CommentMetaRepository
	posts  commentPostReader
	spam   domain.CommentSpamFilter
	now    func() time.Time
}

func NewCommentService(repo domain.CommentRepository, writer domain.CommentWriter, meta domain.CommentMetaRepository, posts commentPostReader, spam domain.CommentSpamFilter) *CommentService {
	return &CommentService{repo: repo, writer: writer, meta: meta, posts: posts, spam: spam, now: time.Now}
}

func (s *CommentService) List(ctx context.Context, filter domain.CommentFilter) ([]domain.Comment, int, error) {
	items, err := s.repo.List(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	total, err := s.repo.Count(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (s *CommentService) Create(ctx context.Context, c domain.Comment) (domain.Comment, error) {
	post, err := s.posts.ByID(ctx, c.PostID)
	if err != nil {
		return domain.Comment{}, err
	}
	if post.Status != "publish" {
		return domain.Comment{}, domain.ErrNotFound
	}
	if strings.EqualFold(post.Excerpt, "closed") {
		return domain.Comment{}, ErrCommentsClosed
	}
	status := commentStatusOK
	if s.spam != nil {
		verdict, err := s.spam.Evaluate(ctx, c, post)
		if err != nil {
			return domain.Comment{}, err
		}
		switch verdict {
		case spamVerdictApprove, "":
			status = commentStatusOK
		case spamVerdictHold:
			status = commentStatusHold
		case spamVerdictSpam:
			status = commentStatusSpam
		default:
			status = commentStatusHold
		}
	}
	c.Status = status
	id, err := s.writer.Create(ctx, c)
	if err != nil {
		return domain.Comment{}, err
	}
	c.ID = id
	return c, nil
}

func (s *CommentService) Approve(ctx context.Context, id int64) error {
	return s.setStatus(ctx, id, commentStatusOK)
}
func (s *CommentService) Unapprove(ctx context.Context, id int64) error {
	return s.setStatus(ctx, id, commentStatusHold)
}
func (s *CommentService) MarkSpam(ctx context.Context, id int64) error {
	return s.setStatus(ctx, id, commentStatusSpam)
}
func (s *CommentService) NotSpam(ctx context.Context, id int64) error {
	return s.setStatus(ctx, id, commentStatusHold)
}

func (s *CommentService) Trash(ctx context.Context, id int64) error {
	comment, err := s.repo.ByID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.meta.Set(ctx, id, wpTrashMetaStatus, comment.Status); err != nil {
		return err
	}
	if err := s.meta.Set(ctx, id, wpTrashMetaTime, fmt.Sprintf("%d", s.now().Unix())); err != nil {
		return err
	}
	return s.writer.UpdateStatus(ctx, id, commentStatusTrash)
}

func (s *CommentService) Untrash(ctx context.Context, id int64) error {
	if _, err := s.repo.ByID(ctx, id); err != nil {
		return err
	}
	status, err := s.meta.Get(ctx, id, wpTrashMetaStatus)
	if errors.Is(err, domain.ErrNotFound) || status == "" {
		status = commentStatusHold
	} else if err != nil {
		return err
	}
	if err := s.writer.UpdateStatus(ctx, id, status); err != nil {
		return err
	}
	if err := s.meta.Delete(ctx, id, wpTrashMetaStatus); err != nil && !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	if err := s.meta.Delete(ctx, id, wpTrashMetaTime); err != nil && !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	return nil
}

func (s *CommentService) setStatus(ctx context.Context, id int64, status string) error {
	if _, err := s.repo.ByID(ctx, id); err != nil {
		return err
	}
	return s.writer.UpdateStatus(ctx, id, status)
}
