package content

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/roboweaver/grimoire/internal/domain"
)

type BasicCommentSpamFilterConfig struct {
	BannedWords []string
	MaxLinks    int
	RateWindow  time.Duration
	RateBurst   int
}

type BasicCommentSpamFilter struct {
	cfg BasicCommentSpamFilterConfig
	mu  sync.Mutex
	ip  map[string][]time.Time
	now func() time.Time
}

func NewBasicCommentSpamFilter(cfg BasicCommentSpamFilterConfig) *BasicCommentSpamFilter {
	if cfg.MaxLinks <= 0 {
		cfg.MaxLinks = 2
	}
	if cfg.RateWindow <= 0 {
		cfg.RateWindow = time.Minute
	}
	if cfg.RateBurst <= 0 {
		cfg.RateBurst = 3
	}
	if len(cfg.BannedWords) == 0 {
		cfg.BannedWords = []string{"viagra", "casino", "loan"}
	}
	return &BasicCommentSpamFilter{cfg: cfg, ip: map[string][]time.Time{}, now: time.Now}
}

func (f *BasicCommentSpamFilter) Evaluate(_ context.Context, c domain.Comment, _ domain.Post) (string, error) {
	content := strings.ToLower(c.Content)
	for _, word := range f.cfg.BannedWords {
		if strings.Contains(content, strings.ToLower(word)) {
			return spamVerdictSpam, nil
		}
	}
	if countLinks(c.Content) > f.cfg.MaxLinks {
		return spamVerdictSpam, nil
	}
	if c.AuthorIP != "" && f.overLimit(c.AuthorIP) {
		return spamVerdictHold, nil
	}
	return spamVerdictApprove, nil
}

func (f *BasicCommentSpamFilter) overLimit(ip string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	cutoff := now.Add(-f.cfg.RateWindow)
	kept := f.ip[ip][:0]
	for _, ts := range f.ip[ip] {
		if ts.After(cutoff) {
			kept = append(kept, ts)
		}
	}
	kept = append(kept, now)
	f.ip[ip] = kept
	return len(kept) > f.cfg.RateBurst
}

func countLinks(s string) int {
	return strings.Count(strings.ToLower(s), "http://") + strings.Count(strings.ToLower(s), "https://")
}
