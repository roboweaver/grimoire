package content

import (
	"context"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/roboweaver/grimoire/internal/domain"
)

type MediaConfig struct {
	UploadsDir   string
	BaseURL      string
	AllowedMIMEs []string
	// MaxUploadSize caps the accepted upload body size in bytes. Callers that
	// leave it unset (e.g. existing tests) get the same 10 MiB default the
	// admin API applies in production (Req 5).
	MaxUploadSize int64
}

type MediaUpload struct {
	Filename string
	MimeType string
	Title    string
	ParentID int64
}

type MediaService struct {
	repo   domain.MediaRepository
	writer domain.MediaWriter
	cfg    MediaConfig
	now    func() time.Time
}

func NewMediaService(repo domain.MediaRepository, writer domain.MediaWriter, cfg MediaConfig) *MediaService {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "/wp-content/uploads"
	}
	if cfg.MaxUploadSize <= 0 {
		cfg.MaxUploadSize = 10 << 20 // 10 MiB
	}
	return &MediaService{repo: repo, writer: writer, cfg: cfg, now: time.Now}
}

func (s *MediaService) List(ctx context.Context, filter domain.MediaFilter) ([]domain.Media, int, error) {
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

func (s *MediaService) Get(ctx context.Context, id int64) (domain.Media, error) {
	return s.repo.ByID(ctx, id)
}

func (s *MediaService) Delete(context.Context, int64) error { return nil }

func (s *MediaService) Config() MediaConfig { return s.cfg }

func (s *MediaService) Attach(ctx context.Context, id, parentID int64) error {
	return s.writer.SetParent(ctx, id, parentID)
}

func (s *MediaService) Store(ctx context.Context, r io.Reader, upload MediaUpload) (domain.Media, error) {
	now := s.now().UTC()
	relDir := filepath.Join(fmt.Sprintf("%04d", now.Year()), fmt.Sprintf("%02d", int(now.Month())))
	base := sanitizeBase(upload.Filename)
	relPath := filepath.Join(relDir, base)
	fullPath := filepath.Join(s.cfg.UploadsDir, relPath)
	fullPath, relPath = dedupePath(fullPath, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return domain.Media{}, err
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return domain.Media{}, err
	}
	if err := os.WriteFile(fullPath, data, 0o644); err != nil {
		return domain.Media{}, err
	}
	title := strings.TrimSpace(upload.Title)
	if title == "" {
		title = titleFromFilename(upload.Filename)
	}
	slug := strings.TrimSuffix(filepath.Base(relPath), filepath.Ext(relPath))
	media := domain.Media{
		Title:    title,
		Filename: filepath.ToSlash(relPath),
		URL:      strings.TrimRight(s.cfg.BaseURL, "/") + "/" + filepath.ToSlash(relPath),
		MimeType: detectedMime(upload.MimeType, upload.Filename),
		Date:     now,
		ParentID: upload.ParentID,
		Slug:     slug,
	}
	id, err := s.writer.Create(ctx, media)
	if err != nil {
		_ = os.Remove(fullPath)
		return domain.Media{}, err
	}
	media.ID = id
	return media, nil
}

func sanitizeBase(name string) string {
	base := filepath.Base(name)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	re := regexp.MustCompile(`[^a-z0-9]+`)
	stem = strings.ToLower(stem)
	stem = re.ReplaceAllString(stem, "-")
	stem = strings.Trim(stem, "-")
	if stem == "" {
		stem = "upload"
	}
	ext = strings.ToLower(ext)
	return stem + ext
}

func titleFromFilename(name string) string {
	base := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	base = strings.ReplaceAll(base, "_", " ")
	base = strings.ReplaceAll(base, "-", " ")
	re := regexp.MustCompile(`[^A-Za-z0-9 ]+`)
	base = re.ReplaceAllString(base, "")
	return strings.TrimSpace(strings.Title(base))
}

func dedupePath(fullPath, relPath string) (string, string) {
	if _, err := os.Stat(fullPath); err != nil {
		return fullPath, relPath
	}
	ext := filepath.Ext(fullPath)
	stem := strings.TrimSuffix(fullPath, ext)
	relStem := strings.TrimSuffix(relPath, ext)
	for i := 1; ; i++ {
		candidate := fmt.Sprintf("%s-%d%s", stem, i, ext)
		if _, err := os.Stat(candidate); err != nil {
			return candidate, fmt.Sprintf("%s-%d%s", relStem, i, ext)
		}
	}
}

func detectedMime(input, filename string) string {
	if input != "" {
		return input
	}
	if ext := filepath.Ext(filename); ext != "" {
		if kind := mime.TypeByExtension(ext); kind != "" {
			return kind
		}
	}
	return "application/octet-stream"
}
