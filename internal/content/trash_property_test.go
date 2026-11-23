package content

import (
	"context"
	"errors"
	"math/rand"
	"reflect"
	"testing"
	"testing/quick"
	"time"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/domain"
)

// --- M22 task 3.1 (RED): Property 7 -----------------------------------------

// trsPropCaps is the pool of capabilities the generator draws subsets from.
var trsPropCaps = []string{
	"edit_posts", "edit_others_posts", "edit_published_posts", "edit_private_posts",
	"delete_posts", "delete_others_posts", "delete_published_posts", "delete_private_posts",
}

var trsPropStatuses = []string{"publish", "draft", "pending", "private"}

// trsPropCase is one generated scenario: a capability set, whether the actor
// owns the row, and the status the row had before it was trashed.
type trsPropCase struct {
	Caps     []string
	Own      bool
	Previous string
}

func (trsPropCase) Generate(r *rand.Rand, _ int) reflect.Value {
	var c trsPropCase
	for _, cp := range trsPropCaps {
		if r.Intn(2) == 0 {
			c.Caps = append(c.Caps, cp)
		}
	}
	c.Own = r.Intn(2) == 0
	c.Previous = trsPropStatuses[r.Intn(len(trsPropStatuses))]
	return reflect.ValueOf(c)
}

// trsPropRun runs op against a service holding one row and reports whether the
// call was authorized (any outcome other than ErrForbidden).
func trsPropRun(t *testing.T, c trsPropCase, status string, op string) bool {
	t.Helper()
	log := &trsLog{}
	author := int64(1)
	if !c.Own {
		author = 2
	}
	row := domain.Post{ID: trsID, Type: "post", Status: status, Author: author,
		Title: "Hello", Slug: "hello", Modified: time.Unix(1, 0)}
	w := &trsWriter{log: log, row: row}
	tr := &trsTrasher{log: log, row: &w.row,
		info: domain.TrashInfo{PreviousStatus: c.Previous, HasPreviousStatus: true}}
	svc := NewPostWriteService(w, WithTrashDays(30), WithPostTrasher(tr))
	actor := auth.NewPrincipal(1, "actor", c.Caps)
	var err error
	switch op {
	case "delete":
		err = svc.Delete(context.Background(), actor, domain.Post{ID: trsID})
	case "update":
		// A live row is edited in place; a trashed row is edited into draft.
		p := row
		if status == "trash" {
			p.Status = "draft"
		}
		err = svc.Update(context.Background(), actor, p, time.Time{})
	}
	return !errors.Is(err, ErrForbidden)
}

// Feature: trash-and-restore, Property 7: A trashed row is authorized as its previous status
//
// **Validates: Requirements 6.2**
func TestPropertyTrashedRowAuthorizedAsPreviousStatus(t *testing.T) {
	cfg := &quick.Config{MaxCount: 200, Rand: rand.New(rand.NewSource(1))}
	for _, op := range []string{"delete", "update"} {
		t.Run(op, func(t *testing.T) {
			prop := func(c trsPropCase) bool {
				live := trsPropRun(t, c, c.Previous, op)
				trashed := trsPropRun(t, c, "trash", op)
				return live == trashed
			}
			if err := quick.Check(prop, cfg); err != nil {
				t.Fatal(err)
			}
		})
	}
}
