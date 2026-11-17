package scheduler

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/roboweaver/grimoire/internal/domain"
)

// gateUpdater is a fakePostUpdater that also implements the optional
// WritableType(string) bool interface the scheduler asserts for (Req 4.3).
type gateUpdater struct {
	*fakePostUpdater
	unwritable map[string]bool
}

func (g *gateUpdater) WritableType(typ string) bool { return !g.unwritable[typ] }

// logRecord captures one slog record.
type logRecord struct {
	level slog.Level
	msg   string
	attrs map[string]any
}

type captureHandler struct {
	mu   *sync.Mutex
	recs *[]logRecord
}

func newCaptureLogger() (*slog.Logger, func() []logRecord) {
	h := &captureHandler{mu: &sync.Mutex{}, recs: &[]logRecord{}}
	return slog.New(h), func() []logRecord {
		h.mu.Lock()
		defer h.mu.Unlock()
		return append([]logRecord(nil), *h.recs...)
	}
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	attrs := map[string]any{}
	r.Attrs(func(a slog.Attr) bool { attrs[a.Key] = a.Value.Any(); return true })
	h.mu.Lock()
	defer h.mu.Unlock()
	*h.recs = append(*h.recs, logRecord{level: r.Level, msg: r.Message, attrs: attrs})
	return nil
}
func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

func gateDue() []domain.Post {
	return []domain.Post{
		{ID: 1, Type: "post", Status: "future"},
		{ID: 2, Type: "shop_order", Status: "future"},
		{ID: 3, Type: "product", Status: "future"},
	}
}

func updatedIDs(calls []updateCall) []int64 {
	ids := make([]int64, 0, len(calls))
	for _, c := range calls {
		ids = append(ids, c.post.ID)
	}
	return ids
}

// Req 4.1, 4.2: unwritable rows are skipped, logged at debug and never at error.
func TestSchedulerGateSkipsUnwritableType(t *testing.T) {
	fake := &gateUpdater{fakePostUpdater: &fakePostUpdater{}, unwritable: map[string]bool{"shop_order": true}}
	logger, records := newCaptureLogger()
	s := New(&fakeFinder{due: gateDue()}, fake, 0, logger)

	s.tick(context.Background())

	ids := updatedIDs(fake.snapshot())
	if len(ids) != 2 || ids[0] != 1 || ids[1] != 3 {
		t.Fatalf("Update called for %v, want [1 3] (shop_order skipped)", ids)
	}
	var debugSkip bool
	for _, r := range records() {
		if r.level >= slog.LevelError {
			t.Errorf("unexpected error-level log for skipped row: %q %v", r.msg, r.attrs)
		}
		if r.level == slog.LevelDebug {
			debugSkip = true
		}
	}
	if !debugSkip {
		t.Error("expected a debug-level log for the skipped shop_order row")
	}
}

// Req 4.3: a fake without WritableType receives Update for every row.
func TestSchedulerGateWithoutMethodUpdatesEveryRow(t *testing.T) {
	fake := &fakePostUpdater{}
	s := New(&fakeFinder{due: gateDue()}, fake, 0, testLogger())

	s.tick(context.Background())

	ids := updatedIDs(fake.snapshot())
	if len(ids) != 3 || ids[0] != 1 || ids[1] != 2 || ids[2] != 3 {
		t.Fatalf("Update called for %v, want [1 2 3]", ids)
	}
}
