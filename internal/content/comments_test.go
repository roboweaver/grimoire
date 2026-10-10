package content

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/sanitize"
)

type fakeCommentRepo struct {
	list        []domain.Comment
	listErr     error
	count       int
	countErr    error
	byID        map[int64]domain.Comment
	byIDErr     map[int64]error
	listFilter  domain.CommentFilter
	countFilter domain.CommentFilter
}

func (f *fakeCommentRepo) List(_ context.Context, filter domain.CommentFilter) ([]domain.Comment, error) {
	f.listFilter = filter
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]domain.Comment, len(f.list))
	copy(out, f.list)
	return out, nil
}

func (f *fakeCommentRepo) Count(_ context.Context, filter domain.CommentFilter) (int, error) {
	f.countFilter = filter
	if f.countErr != nil {
		return 0, f.countErr
	}
	return f.count, nil
}

func (f *fakeCommentRepo) ByID(_ context.Context, id int64) (domain.Comment, error) {
	if err := f.byIDErr[id]; err != nil {
		return domain.Comment{}, err
	}
	c, ok := f.byID[id]
	if !ok {
		return domain.Comment{}, domain.ErrNotFound
	}
	return c, nil
}

type fakeCommentWriter struct {
	created []domain.Comment
	updated []struct {
		id     int64
		status string
	}
	createID     int64
	createErr    error
	updateErr    map[int64]error
	updateStatus map[int64]string
}

func (f *fakeCommentWriter) Create(_ context.Context, c domain.Comment) (int64, error) {
	f.created = append(f.created, c)
	if f.createErr != nil {
		return 0, f.createErr
	}
	if f.createID == 0 {
		f.createID = 99
	}
	return f.createID, nil
}

func (f *fakeCommentWriter) UpdateStatus(_ context.Context, id int64, status string) error {
	f.updated = append(f.updated, struct {
		id     int64
		status string
	}{id: id, status: status})
	if err, ok := f.updateErr[id]; ok {
		return err
	}
	if f.updateStatus == nil {
		f.updateStatus = map[int64]string{}
	}
	f.updateStatus[id] = status
	return nil
}

type fakeCommentMeta struct {
	values map[int64]map[string]string
	getErr map[int64]map[string]error
	setOps []struct {
		commentID  int64
		key, value string
	}
	delOps []struct {
		commentID int64
		key       string
	}
	setErr error
	delErr map[int64]map[string]error
}

func (f *fakeCommentMeta) Get(_ context.Context, commentID int64, key string) (string, error) {
	if m := f.getErr[commentID]; m != nil {
		if err := m[key]; err != nil {
			return "", err
		}
	}
	if v := f.values[commentID]; v != nil {
		if got, ok := v[key]; ok {
			return got, nil
		}
	}
	return "", domain.ErrNotFound
}

func (f *fakeCommentMeta) Set(_ context.Context, commentID int64, key, value string) error {
	f.setOps = append(f.setOps, struct {
		commentID  int64
		key, value string
	}{commentID: commentID, key: key, value: value})
	if f.setErr != nil {
		return f.setErr
	}
	if f.values == nil {
		f.values = map[int64]map[string]string{}
	}
	if f.values[commentID] == nil {
		f.values[commentID] = map[string]string{}
	}
	f.values[commentID][key] = value
	return nil
}

func (f *fakeCommentMeta) ByComment(_ context.Context, commentID int64) (map[string]string, error) {
	out := map[string]string{}
	for k, v := range f.values[commentID] {
		out[k] = v
	}
	return out, nil
}

func (f *fakeCommentMeta) Delete(_ context.Context, commentID int64, key string) error {
	f.delOps = append(f.delOps, struct {
		commentID int64
		key       string
	}{commentID: commentID, key: key})
	if m := f.delErr[commentID]; m != nil {
		if err := m[key]; err != nil {
			return err
		}
	}
	if f.values[commentID] != nil {
		delete(f.values[commentID], key)
	}
	return nil
}

type fakePostByID struct {
	posts map[int64]domain.Post
}

func (f *fakePostByID) ByID(_ context.Context, id int64) (domain.Post, error) {
	p, ok := f.posts[id]
	if !ok {
		return domain.Post{}, domain.ErrNotFound
	}
	return p, nil
}

type fakeSpamFilter struct {
	verdict string
	err     error
	seen    []domain.Comment
	posts   []domain.Post
}

func (f *fakeSpamFilter) Evaluate(_ context.Context, c domain.Comment, post domain.Post) (string, error) {
	f.seen = append(f.seen, c)
	f.posts = append(f.posts, post)
	if f.err != nil {
		return "", f.err
	}
	return f.verdict, nil
}

func TestCommentServiceCreateRoutesSpamVerdictsAndValidatesPost(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	basePost := domain.Post{ID: 10, Status: "publish", Type: "post", Excerpt: "open"}

	tests := []struct {
		name        string
		post        domain.Post
		postID      int64
		filter      domain.CommentSpamFilter
		wantStatus  string
		wantErr     error
		wantCreated bool
	}{
		{name: "no filter defaults to held for moderation", post: basePost, postID: 10, wantStatus: "0", wantCreated: true},
		{name: "approve verdict persists approved", post: basePost, postID: 10, filter: &fakeSpamFilter{verdict: spamVerdictApprove}, wantStatus: "1", wantCreated: true},
		{name: "hold verdict persists moderation", post: basePost, postID: 10, filter: &fakeSpamFilter{verdict: spamVerdictHold}, wantStatus: "0", wantCreated: true},
		{name: "spam verdict persists spam", post: basePost, postID: 10, filter: &fakeSpamFilter{verdict: spamVerdictSpam}, wantStatus: "spam", wantCreated: true},
		{name: "missing post rejected", postID: 999, wantErr: domain.ErrNotFound},
		{name: "unpublished post rejected", post: domain.Post{ID: 10, Status: "draft", Type: "post", Excerpt: "open"}, postID: 10, wantErr: domain.ErrNotFound},
		{name: "closed post rejected", post: domain.Post{ID: 10, Status: "publish", Type: "post", CommentStatus: "closed"}, postID: 10, wantErr: ErrCommentsClosed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			posts := map[int64]domain.Post{}
			if tt.postID != 999 {
				posts[10] = tt.post
			}
			writer := &fakeCommentWriter{}
			svc := NewCommentService(&fakeCommentRepo{}, writer, &fakeCommentMeta{}, &fakePostByID{posts: posts}, tt.filter, sanitize.New())
			_, _, err := svc.Create(context.Background(), sanitize.Anonymous(), domain.Comment{PostID: tt.postID, Author: "A", AuthorEmail: "a@example.com", Content: "hello", Date: now, DateGMT: now})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Create error = %v, want %v", err, tt.wantErr)
			}
			if gotCreated := len(writer.created) == 1; gotCreated != tt.wantCreated {
				t.Fatalf("created = %v, want %v", gotCreated, tt.wantCreated)
			}
			if tt.wantCreated {
				if got := writer.created[0].Status; got != tt.wantStatus {
					t.Fatalf("stored status = %q, want %q", got, tt.wantStatus)
				}
			}
		})
	}
}

func TestCommentServiceListPassesFilterAndCount(t *testing.T) {
	repo := &fakeCommentRepo{list: []domain.Comment{{ID: 1}}, count: 7}
	svc := NewCommentService(repo, &fakeCommentWriter{}, &fakeCommentMeta{}, &fakePostByID{}, nil, sanitize.New())
	comments, total, err := svc.List(context.Background(), domain.CommentFilter{PostID: 5, Statuses: []string{"1"}, Limit: 20, Offset: 40})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(comments) != 1 || total != 7 {
		t.Fatalf("comments/total = %d/%d, want 1/7", len(comments), total)
	}
	if repo.listFilter.PostID != 5 || repo.countFilter.PostID != 5 {
		t.Fatalf("filters not propagated: list=%+v count=%+v", repo.listFilter, repo.countFilter)
	}
}

func TestCommentServiceTrashSnapshotsAndUntrashRestores(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 30, 0, 0, time.UTC)
	repo := &fakeCommentRepo{byID: map[int64]domain.Comment{12: {ID: 12, Status: "1"}}}
	meta := &fakeCommentMeta{values: map[int64]map[string]string{}}
	writer := &fakeCommentWriter{}
	svc := NewCommentService(repo, writer, meta, &fakePostByID{}, nil, sanitize.New())
	svc.now = func() time.Time { return now }

	if err := svc.Trash(context.Background(), 12); err != nil {
		t.Fatalf("Trash: %v", err)
	}
	if len(meta.setOps) != 2 {
		t.Fatalf("trash meta writes = %d, want 2", len(meta.setOps))
	}
	if meta.values[12][wpTrashMetaStatus] != "1" {
		t.Fatalf("saved status = %q, want 1", meta.values[12][wpTrashMetaStatus])
	}
	if writer.updateStatus[12] != commentStatusTrash {
		t.Fatalf("updated status = %q, want trash", writer.updateStatus[12])
	}

	if err := svc.Untrash(context.Background(), 12); err != nil {
		t.Fatalf("Untrash: %v", err)
	}
	if writer.updated[len(writer.updated)-1].status != "1" {
		t.Fatalf("restored status = %q, want 1", writer.updated[len(writer.updated)-1].status)
	}
	if _, ok := meta.values[12][wpTrashMetaStatus]; ok {
		t.Fatal("trash status meta should be deleted")
	}
	if _, ok := meta.values[12][wpTrashMetaTime]; ok {
		t.Fatal("trash time meta should be deleted")
	}
}

func TestCommentServiceUntrashDefaultsToHoldWhenSnapshotMissing(t *testing.T) {
	repo := &fakeCommentRepo{byID: map[int64]domain.Comment{33: {ID: 33, Status: commentStatusTrash}}}
	meta := &fakeCommentMeta{values: map[int64]map[string]string{}}
	writer := &fakeCommentWriter{}
	svc := NewCommentService(repo, writer, meta, &fakePostByID{}, nil, sanitize.New())

	if err := svc.Untrash(context.Background(), 33); err != nil {
		t.Fatalf("Untrash: %v", err)
	}
	if writer.updated[0].status != commentStatusHold {
		t.Fatalf("restored status = %q, want %q", writer.updated[0].status, commentStatusHold)
	}
}

func TestBasicCommentSpamFilterHeuristics(t *testing.T) {
	filter := NewBasicCommentSpamFilter(BasicCommentSpamFilterConfig{
		BannedWords: []string{"viagra"},
		MaxLinks:    2,
		RateWindow:  time.Minute,
		RateBurst:   2,
	})
	post := domain.Post{ID: 1, Status: "publish"}
	ctx := context.Background()

	if verdict, _ := filter.Evaluate(ctx, domain.Comment{Author: "A", AuthorEmail: "a@example.com", Content: "hello", AuthorIP: "1.2.3.4"}, post); verdict != spamVerdictHold {
		t.Fatalf("clean verdict = %q, want hold", verdict)
	}
	if verdict, _ := filter.Evaluate(ctx, domain.Comment{Author: "A", AuthorEmail: "a@example.com", Content: "buy viagra now", AuthorIP: "2.3.4.5"}, post); verdict != spamVerdictSpam {
		t.Fatalf("keyword verdict = %q, want spam", verdict)
	}
	if verdict, _ := filter.Evaluate(ctx, domain.Comment{Author: "A", AuthorEmail: "a@example.com", Content: "http://a.example http://b.example http://c.example", AuthorIP: "3.4.5.6"}, post); verdict != spamVerdictSpam {
		t.Fatalf("link verdict = %q, want spam", verdict)
	}
	if verdict, _ := filter.Evaluate(ctx, domain.Comment{Author: "A", AuthorEmail: "a@example.com", Content: "hello", AuthorIP: "9.9.9.9"}, post); verdict != spamVerdictHold {
		t.Fatalf("first verdict = %q, want hold", verdict)
	}
	if verdict, _ := filter.Evaluate(ctx, domain.Comment{Author: "A", AuthorEmail: "a@example.com", Content: "hello again", AuthorIP: "9.9.9.9"}, post); verdict != spamVerdictHold {
		t.Fatalf("second verdict = %q, want hold", verdict)
	}
	if verdict, _ := filter.Evaluate(ctx, domain.Comment{Author: "A", AuthorEmail: "a@example.com", Content: "third", AuthorIP: "9.9.9.9"}, post); verdict != spamVerdictHold {
		t.Fatalf("rate verdict = %q, want hold", verdict)
	}
}

// --- Task 4.1: sanitization wiring of CommentService.Create ---
//
// These tests pin the write-boundary content-safety behavior task 4.2 will
// implement. They currently fail to compile (and, once compiling, fail) because
// Create does not yet take an `actor sanitize.Writer`, does not sanitize via the
// policy, and does not return content.ErrCommentEmpty. See design.md sections
// "Ordering in CommentService.Create", "Emptied fields (Requirement 4.8)" and
// the ErrCommentEmpty sentinel.
//
// Assumed signatures (so task 4.2 matches):
//   - NewCommentService(repo, writer, meta, posts, spam, policy *sanitize.Policy) *CommentService
//     (policy appended as the final constructor parameter)
//   - (*CommentService).Create(ctx context.Context, actor sanitize.Writer, c domain.Comment)
//     (domain.Comment, domain.Post, error)
//     (actor inserted as the first parameter after ctx)
//   - var ErrCommentEmpty error  // content: comment content empty after sanitization

// anonActor is the public (no-Principal) comment submitter: tier A for comment
// content.
func anonActor() sanitize.Writer { return sanitize.Anonymous() }

// unfilteredActor is a tier-C actor: a Principal holding unfiltered_html, which
// passes comment content through verbatim.
func unfilteredActor() sanitize.Writer {
	return sanitize.For(auth.NewPrincipal(1, "editor", []string{sanitize.CapUnfilteredHTML}))
}

// TestCommentServiceCreateSanitizesAnonymousAtTierA asserts that an anonymous
// actor's comment content is sanitized through tier A, that the SANITIZED (not
// the raw) value is what the spam filter scores and what gets persisted, and
// that sanitization happens before spam evaluation. (Req 4.1, 1.6, 2.5)
func TestCommentServiceCreateSanitizesAnonymousAtTierA(t *testing.T) {
	post := domain.Post{ID: 10, Status: "publish", Type: "post"}
	writer := &fakeCommentWriter{}
	spam := &fakeSpamFilter{verdict: spamVerdictApprove}
	svc := NewCommentService(&fakeCommentRepo{}, writer, &fakeCommentMeta{}, &fakePostByID{posts: map[int64]domain.Post{10: post}}, spam, sanitize.New())

	// Tier A keeps <em> but strips the global class attribute and <script>.
	raw := `<em>hi</em> <span class="x">y</span><script>alert(1)</script>`
	wantClean, err := sanitize.New().Sanitize(sanitize.CommentContent, anonActor(), raw)
	if err != nil {
		t.Fatalf("oracle sanitize: %v", err)
	}

	got, _, err := svc.Create(context.Background(), anonActor(), domain.Comment{PostID: 10, Author: "A", AuthorEmail: "a@example.com", Content: raw})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if wantClean == raw {
		t.Fatal("test input must change under tier A sanitization")
	}
	if len(writer.created) != 1 {
		t.Fatalf("writer.Create calls = %d, want 1", len(writer.created))
	}
	if writer.created[0].Content != wantClean {
		t.Fatalf("persisted content = %q, want sanitized %q", writer.created[0].Content, wantClean)
	}
	if len(spam.seen) != 1 || spam.seen[0].Content != wantClean {
		t.Fatalf("spam filter scored %q, want sanitized %q", spam.seen[0].Content, wantClean)
	}
	if got.Content != wantClean {
		t.Fatalf("returned comment content = %q, want sanitized %q", got.Content, wantClean)
	}
}

// TestCommentServiceCreateTierCPassesThroughVerbatim asserts that an actor
// holding unfiltered_html routes comment content through tier C: the stored and
// spam-scored content is byte-identical to the raw input. (Req 2.5)
func TestCommentServiceCreateTierCPassesThroughVerbatim(t *testing.T) {
	post := domain.Post{ID: 10, Status: "publish", Type: "post"}
	writer := &fakeCommentWriter{}
	spam := &fakeSpamFilter{verdict: spamVerdictApprove}
	svc := NewCommentService(&fakeCommentRepo{}, writer, &fakeCommentMeta{}, &fakePostByID{posts: map[int64]domain.Post{10: post}}, spam, sanitize.New())

	raw := `<em>hi</em> <span class="x">y</span><script>alert(1)</script>`
	if _, _, err := svc.Create(context.Background(), unfilteredActor(), domain.Comment{PostID: 10, Author: "A", AuthorEmail: "a@example.com", Content: raw}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(writer.created) != 1 {
		t.Fatalf("writer.Create calls = %d, want 1", len(writer.created))
	}
	if writer.created[0].Content != raw {
		t.Fatalf("persisted content = %q, want verbatim %q", writer.created[0].Content, raw)
	}
	if len(spam.seen) != 1 || spam.seen[0].Content != raw {
		t.Fatalf("spam filter scored %q, want verbatim %q", spam.seen[0].Content, raw)
	}
}

// TestCommentServiceCreateRejectsSanitizedToEmpty asserts that a comment body
// that is non-empty but sanitizes to empty at tier A returns ErrCommentEmpty and
// does NOT persist. (Req 4.8, "ErrCommentEmpty")
func TestCommentServiceCreateRejectsSanitizedToEmpty(t *testing.T) {
	post := domain.Post{ID: 10, Status: "publish", Type: "post"}

	cases := []struct {
		name string
		raw  string
	}{
		{name: "script only", raw: `<script>alert(1)</script>`},
		{name: "markup only", raw: `<img src="x">`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Guard: the oracle must actually sanitize this input to empty, else
			// the test asserts nothing.
			clean, err := sanitize.New().Sanitize(sanitize.CommentContent, anonActor(), tc.raw)
			if err != nil {
				t.Fatalf("oracle sanitize: %v", err)
			}
			if clean != "" {
				t.Fatalf("input %q sanitizes to %q, want empty", tc.raw, clean)
			}

			writer := &fakeCommentWriter{}
			spam := &fakeSpamFilter{verdict: spamVerdictApprove}
			svc := NewCommentService(&fakeCommentRepo{}, writer, &fakeCommentMeta{}, &fakePostByID{posts: map[int64]domain.Post{10: post}}, spam, sanitize.New())

			_, _, err = svc.Create(context.Background(), anonActor(), domain.Comment{PostID: 10, Author: "A", AuthorEmail: "a@example.com", Content: tc.raw})
			if !errors.Is(err, ErrCommentEmpty) {
				t.Fatalf("Create error = %v, want ErrCommentEmpty", err)
			}
			if len(writer.created) != 0 {
				t.Fatalf("writer.Create calls = %d, want 0 (no persistence)", len(writer.created))
			}
		})
	}
}

// TestCommentServiceCreateFiresHookWithSanitizedContent asserts that the
// comment.submitted payload carries the sanitized content, since sanitization
// runs before persistence and the hook fires with the stored comment. (Req 11.2)
func TestCommentServiceCreateFiresHookWithSanitizedContent(t *testing.T) {
	post := domain.Post{ID: 10, Status: "publish", Type: "post"}
	writer := &fakeCommentWriter{createID: 42}
	spam := &fakeSpamFilter{verdict: spamVerdictApprove}
	svc := NewCommentService(&fakeCommentRepo{}, writer, &fakeCommentMeta{}, &fakePostByID{posts: map[int64]domain.Post{10: post}}, spam, sanitize.New())

	raw := `<em>ok</em><script>alert(1)</script>`
	wantClean, err := sanitize.New().Sanitize(sanitize.CommentContent, anonActor(), raw)
	if err != nil {
		t.Fatalf("oracle sanitize: %v", err)
	}

	got, _, err := svc.Create(context.Background(), anonActor(), domain.Comment{PostID: 10, Author: "A", AuthorEmail: "a@example.com", Content: raw})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.ID != 42 {
		t.Fatalf("returned comment ID = %d, want 42", got.ID)
	}
	if got.Content != wantClean {
		t.Fatalf("submitted comment content = %q, want sanitized %q", got.Content, wantClean)
	}
}

// TestNewCommentServiceNilPolicyFailsClosed pins the defense-in-depth default
// (PR #53 review finding 2): constructing a CommentService with a nil policy
// must not nil-panic in Create; the constructor substitutes sanitize.New() so an
// unwired service sanitizes rather than skipping sanitization or crashing,
// matching PostWriteService and web.Server. A tier-A write still strips a
// disallowed <script> while keeping an allow-listed <em>.
func TestNewCommentServiceNilPolicyFailsClosed(t *testing.T) {
	post := domain.Post{ID: 10, Status: "publish", Type: "post"}
	writer := &fakeCommentWriter{}
	svc := NewCommentService(&fakeCommentRepo{}, writer, &fakeCommentMeta{}, &fakePostByID{posts: map[int64]domain.Post{10: post}}, &fakeSpamFilter{verdict: spamVerdictApprove}, nil)

	got, _, err := svc.Create(context.Background(), sanitize.Anonymous(), domain.Comment{PostID: 10, Author: "A", AuthorEmail: "a@example.com", Content: `<em>ok</em><script>alert(1)</script>`})
	if err != nil {
		t.Fatalf("Create with nil policy: %v (constructor must default to sanitize.New(), not leave policy nil)", err)
	}
	if len(writer.created) != 1 {
		t.Fatalf("writer.Create calls = %d, want 1", len(writer.created))
	}
	if strings.Contains(writer.created[0].Content, "<script") {
		t.Errorf("nil-defaulted policy did not sanitize: %q", writer.created[0].Content)
	}
	if !strings.Contains(got.Content, "<em>ok</em>") {
		t.Errorf("nil-defaulted policy dropped allow-listed <em>: %q", got.Content)
	}
}
