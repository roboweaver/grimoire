package content

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/roboweaver/grimoire/internal/php"
	"github.com/roboweaver/grimoire/internal/safepath"
)

// removeMediaFile is the unlink seam; tests replace it.
var removeMediaFile = os.Remove

// thumbSharedChecker is the narrow lookup filterSharedThumb needs: whether any
// attachment other than excludeID still references filename in its
// _wp_attachment_metadata (WordPress's wp_delete_attachment_files thumb guard).
type thumbSharedChecker interface {
	ThumbSharedElsewhere(ctx context.Context, excludeID int64, filename string) (bool, error)
}

// mediaFileCandidates decodes an attachment's _wp_attachment_metadata and
// _wp_attachment_backup_sizes into the filenames (relative to the main file's
// directory) that wp_delete_attachment_files removes besides the main file:
// every sizes[*].file, the named keys original_image, source_image,
// animated_video, animated_video_poster and thumb when non-empty, and every
// backup_sizes[*].file. A malformed or empty payload contributes nothing and
// never errors; the two payloads are decoded independently.
func mediaFileCandidates(metaRaw, backupRaw string) []string {
	var out []string
	if top, ok := mediaDecodeMap(metaRaw); ok {
		if sizes, ok := top["sizes"].(map[string]any); ok {
			out = appendEntryFiles(out, sizes)
		}
		for _, k := range []string{"original_image", "source_image", "animated_video", "animated_video_poster", "thumb"} {
			if f, _ := top[k].(string); f != "" {
				out = append(out, f)
			}
		}
	}
	if backup, ok := mediaDecodeMap(backupRaw); ok {
		out = appendEntryFiles(out, backup)
	}
	return out
}

func mediaDecodeMap(raw string) (map[string]any, bool) {
	if raw == "" {
		return nil, false
	}
	v, err := php.Unserialize(raw)
	if err != nil {
		return nil, false
	}
	m, ok := v.(map[string]any)
	return m, ok
}

// appendEntryFiles appends the non-empty "file" of each map-valued entry.
func appendEntryFiles(out []string, entries map[string]any) []string {
	for _, e := range entries {
		entry, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if f, _ := entry["file"].(string); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// mediaThumbName returns the metadata's top-level "thumb" filename, or "" when
// the payload is empty, malformed or has no thumb.
func mediaThumbName(metaRaw string) string {
	top, ok := mediaDecodeMap(metaRaw)
	if !ok {
		return ""
	}
	f, _ := top["thumb"].(string)
	return f
}

// filterSharedThumb drops the thumb candidate when another attachment's
// metadata still references it, matching WordPress, which checks sharing for
// the thumb entry only. A lookup error also drops it: leaving a file on disk is
// the safe failure. An empty thumb or a nil meta returns candidates unchanged.
func filterSharedThumb(ctx context.Context, meta thumbSharedChecker, id int64, thumb string, candidates []string) []string {
	if meta == nil || thumb == "" {
		return candidates
	}
	shared, err := meta.ThumbSharedElsewhere(ctx, id, thumb)
	if err == nil && !shared {
		return candidates
	}
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if c != thumb {
			out = append(out, c)
		}
	}
	return out
}

// deleteMediaFiles removes mainFile and every candidate (relative to the main
// file's directory) under cfg.UploadsDir. It is best effort: paths whose
// symlink-resolved location is outside the uploads directory are skipped,
// missing files are ignored, and unlink errors are logged and never returned.
// A nil log is valid.
func deleteMediaFiles(cfg MediaConfig, mainFile string, candidates []string, log *slog.Logger) {
	root, err := filepath.Abs(cfg.UploadsDir)
	if err == nil {
		root, err = filepath.EvalSymlinks(root)
	}
	if err != nil {
		if log != nil {
			log.Warn("media files: uploads dir unavailable", "dir", cfg.UploadsDir, "err", err)
		}
		return
	}
	mainPath := mainFile
	if !filepath.IsAbs(mainPath) {
		mainPath = filepath.Join(root, filepath.FromSlash(mainFile))
	}
	mainDir := filepath.Dir(mainPath)
	paths := []string{mainPath}
	for _, c := range candidates {
		if filepath.IsAbs(c) {
			paths = append(paths, c)
		} else {
			paths = append(paths, filepath.Join(mainDir, filepath.FromSlash(c)))
		}
	}
	for _, p := range paths {
		resolved, ok, rerr := safepath.ResolveWithinRoot(filepath.Clean(p), root)
		if rerr != nil || !ok {
			if log != nil {
				log.Warn("media files: skipping path outside uploads dir", "path", p, "err", rerr)
			}
			continue
		}
		if err := removeMediaFile(resolved); err != nil && !errors.Is(err, fs.ErrNotExist) {
			if log != nil {
				log.Warn("media files: unlink failed", "path", resolved, "err", err)
			}
		}
	}
}
