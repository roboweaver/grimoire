package content

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/domain"
)

type fakeRESTPostTerms struct {
	byPost map[int64]map[string][]int64
}

func (f *fakeRESTPostTerms) TermsForPost(_ context.Context, postID int64, taxonomy string) ([]int64, error) {
	if m, ok := f.byPost[postID]; ok {
		return m[taxonomy], nil
	}
	return nil, nil
}

type fakeRESTPostMeta struct {
	featured map[int64]int64
	attach   map[int64]string
}

func (f *fakeRESTPostMeta) FeaturedMediaID(_ context.Context, postID int64) (int64, error) {
	return f.featured[postID], nil
}

func (f *fakeRESTPostMeta) AttachmentMetadata(_ context.Context, postID int64) (string, error) {
	return f.attach[postID], nil
}

type fakeRESTUserMeta struct {
	values map[int64]map[string]string
}

func (f *fakeRESTUserMeta) Get(_ context.Context, userID int64, key string) (string, error) {
	if m, ok := f.values[userID]; ok {
		if v, ok := m[key]; ok {
			return v, nil
		}
	}
	return "", domain.ErrNotFound
}

func (f *fakeRESTUserMeta) Set(_ context.Context, userID int64, key, value string) error {
	if f.values == nil {
		f.values = map[int64]map[string]string{}
	}
	if f.values[userID] == nil {
		f.values[userID] = map[string]string{}
	}
	f.values[userID][key] = value
	return nil
}

func (f *fakeRESTUserMeta) ByUser(_ context.Context, userID int64) (map[string]string, error) {
	return f.values[userID], nil
}

func newTestMapper() (*RESTMapper, *fakeRESTPostTerms, *fakeRESTPostMeta, *fakeRESTUserMeta) {
	terms := &fakeRESTPostTerms{byPost: map[int64]map[string][]int64{}}
	meta := &fakeRESTPostMeta{featured: map[int64]int64{}, attach: map[int64]string{}}
	users := &fakeRESTUserMeta{values: map[int64]map[string]string{}}
	return NewRESTMapper(terms, meta, users, "wp_"), terms, meta, users
}

func samplePost() domain.Post {
	loc := time.UTC
	return domain.Post{
		ID:            42,
		Author:        7,
		Date:          time.Date(2024, 3, 5, 10, 30, 0, 0, loc),
		DateGMT:       time.Date(2024, 3, 5, 10, 30, 0, 0, loc),
		Modified:      time.Date(2024, 3, 6, 11, 0, 0, 0, loc),
		ModifiedGMT:   time.Date(2024, 3, 6, 11, 0, 0, 0, loc),
		Content:       "<p>hello</p>",
		Title:         "Hello World",
		Excerpt:       "",
		Status:        "publish",
		CommentStatus: "open",
		PingStatus:    "open",
		Slug:          "hello-world",
		Type:          "post",
		Password:      "",
		GUID:          "http://example.test/?p=42",
	}
}

func TestRESTMapperPostFieldsAndFormats(t *testing.T) {
	mapper, terms, meta, _ := newTestMapper()
	terms.byPost[42] = map[string][]int64{
		TaxonomyCategory: {3, 1},
		taxonomyPostTag:  {9},
	}
	meta.featured[42] = 55

	got, err := mapper.Post(context.Background(), samplePost())
	if err != nil {
		t.Fatalf("Post: %v", err)
	}

	if got.ID != 42 {
		t.Errorf("ID = %d, want 42", got.ID)
	}
	if got.Date != "2024-03-05T10:30:00" {
		t.Errorf("Date = %q", got.Date)
	}
	if got.DateGMT != "2024-03-05T10:30:00" {
		t.Errorf("DateGMT = %q", got.DateGMT)
	}
	if got.Modified != "2024-03-06T11:00:00" {
		t.Errorf("Modified = %q", got.Modified)
	}
	if got.ModifiedGMT != "2024-03-06T11:00:00" {
		t.Errorf("ModifiedGMT = %q", got.ModifiedGMT)
	}
	if got.Slug != "hello-world" {
		t.Errorf("Slug = %q", got.Slug)
	}
	if got.Status != "publish" {
		t.Errorf("Status = %q", got.Status)
	}
	if got.Type != "post" {
		t.Errorf("Type = %q", got.Type)
	}
	if got.Link != "/hello-world" {
		t.Errorf("Link = %q", got.Link)
	}
	if got.GUID.Rendered != "http://example.test/?p=42" {
		t.Errorf("GUID.Rendered = %q", got.GUID.Rendered)
	}
	if got.Title.Rendered != "Hello World" {
		t.Errorf("Title.Rendered = %q", got.Title.Rendered)
	}
	if got.Content.Rendered != "<p>hello</p>" {
		t.Errorf("Content.Rendered = %q", got.Content.Rendered)
	}
	if got.Content.Protected {
		t.Errorf("Content.Protected = true, want false")
	}
	if got.Author != 7 {
		t.Errorf("Author = %d, want 7", got.Author)
	}
	if got.FeaturedMedia != 55 {
		t.Errorf("FeaturedMedia = %d, want 55", got.FeaturedMedia)
	}
	if got.CommentStatus != "open" {
		t.Errorf("CommentStatus = %q", got.CommentStatus)
	}
	if got.PingStatus != "open" {
		t.Errorf("PingStatus = %q", got.PingStatus)
	}
	if len(got.Categories) != 2 || got.Categories[0] != 3 || got.Categories[1] != 1 {
		t.Errorf("Categories = %v", got.Categories)
	}
	if len(got.Tags) != 1 || got.Tags[0] != 9 {
		t.Errorf("Tags = %v", got.Tags)
	}
}

func TestRESTMapperPostContentProtectedWhenPasswordSet(t *testing.T) {
	mapper, _, _, _ := newTestMapper()
	p := samplePost()
	p.Password = "secret"

	got, err := mapper.Post(context.Background(), p)
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if !got.Content.Protected {
		t.Errorf("Content.Protected = false, want true")
	}
}

func TestRESTMapperPostCategoriesTagsAreEmptySliceNotNilWhenAbsent(t *testing.T) {
	mapper, _, _, _ := newTestMapper()

	got, err := mapper.Post(context.Background(), samplePost())
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if got.Categories == nil {
		t.Error("Categories is nil, want empty non-nil slice")
	}
	if got.Tags == nil {
		t.Error("Tags is nil, want empty non-nil slice")
	}

	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if cats, ok := raw["categories"].([]any); !ok || len(cats) != 0 {
		t.Errorf(`json "categories" = %v, want []`, raw["categories"])
	}
	if tags, ok := raw["tags"].([]any); !ok || len(tags) != 0 {
		t.Errorf(`json "tags" = %v, want []`, raw["tags"])
	}
}

func TestRESTMapperPageOmitsCategoriesAndTagsFromJSON(t *testing.T) {
	mapper, _, _, _ := newTestMapper()
	p := samplePost()
	p.Type = "page"
	p.Slug = "about"

	got, err := mapper.Page(context.Background(), p)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	if got.Type != "page" {
		t.Errorf("Type = %q", got.Type)
	}
	if got.Link != "/about" {
		t.Errorf("Link = %q", got.Link)
	}

	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, ok := raw["categories"]; ok {
		t.Errorf(`json has "categories" key, want absent for pages`)
	}
	if _, ok := raw["tags"]; ok {
		t.Errorf(`json has "tags" key, want absent for pages`)
	}
}

func TestRESTMapperDoesNotFilterOrErrorOnNonPublishedPost(t *testing.T) {
	// REST authorization/visibility gating belongs to the web layer (Req
	// 2.4); the content-layer mapper must map whatever domain.Post it's
	// given, including drafts, without itself erroring or refusing.
	mapper, _, _, _ := newTestMapper()
	p := samplePost()
	p.Status = "draft"

	got, err := mapper.Post(context.Background(), p)
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if got.Status != "draft" {
		t.Errorf("Status = %q, want draft", got.Status)
	}
}

func TestRESTCommentMapping(t *testing.T) {
	c := domain.Comment{
		ID:        9,
		PostID:    42,
		Author:    "Jane <b>Doe</b>",
		AuthorURL: "https://example.test",
		Date:      time.Date(2024, 3, 5, 12, 0, 0, 0, time.UTC),
		DateGMT:   time.Date(2024, 3, 5, 12, 0, 0, 0, time.UTC),
		Content:   "<script>alert(1)</script>hi",
		Status:    commentStatusOK,
		Parent:    0,
	}

	got := Comment(c)

	if got.ID != 9 || got.Post != 42 || got.Parent != 0 {
		t.Errorf("got = %+v", got)
	}
	if got.AuthorName != "Jane <b>Doe</b>" {
		t.Errorf("AuthorName = %q", got.AuthorName)
	}
	if got.Date != "2024-03-05T12:00:00" {
		t.Errorf("Date = %q", got.Date)
	}
	if got.Content.Rendered != "&lt;script&gt;alert(1)&lt;/script&gt;hi" {
		t.Errorf("Content.Rendered = %q, want HTML-escaped", got.Content.Rendered)
	}
	if got.Link != "/?p=42#comment-9" {
		t.Errorf("Link = %q", got.Link)
	}
	if got.Status != "approved" {
		t.Errorf("Status = %q, want approved", got.Status)
	}
}

func TestRESTCommentStatusMapping(t *testing.T) {
	cases := map[string]string{
		commentStatusHold:  "hold",
		commentStatusOK:    "approved",
		commentStatusSpam:  "spam",
		commentStatusTrash: "trash",
		"unexpected":       "unexpected",
	}
	for raw, want := range cases {
		if got := restCommentStatus(raw); got != want {
			t.Errorf("restCommentStatus(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestRESTMapperMediaFields(t *testing.T) {
	mapper, _, meta, _ := newTestMapper()
	meta.attach[3] = `a:2:{s:5:"width";i:800;s:6:"height";i:600;}`

	m := domain.Media{
		ID:       3,
		Title:    "A Photo",
		Filename: "2024/03/a-photo.jpg",
		URL:      "/wp-content/uploads/2024/03/a-photo.jpg",
		MimeType: "image/jpeg",
		Date:     time.Date(2024, 3, 5, 9, 0, 0, 0, time.UTC),
		ParentID: 42,
		Slug:     "a-photo",
		AuthorID: 7,
	}

	got, err := mapper.Media(context.Background(), m)
	if err != nil {
		t.Fatalf("Media: %v", err)
	}
	if got.ID != 3 {
		t.Errorf("ID = %d", got.ID)
	}
	if got.Slug != "a-photo" {
		t.Errorf("Slug = %q", got.Slug)
	}
	if got.Type != "attachment" {
		t.Errorf("Type = %q, want attachment", got.Type)
	}
	if got.Author != 7 {
		t.Errorf("Author = %d, want 7", got.Author)
	}
	if got.SourceURL != m.URL {
		t.Errorf("SourceURL = %q, want %q", got.SourceURL, m.URL)
	}
	if got.Link != m.URL {
		t.Errorf("Link = %q, want %q (no attachment permalink page)", got.Link, m.URL)
	}
	if got.Post != 42 {
		t.Errorf("Post = %d, want 42", got.Post)
	}
	if got.MediaDetails.Width != 800 || got.MediaDetails.Height != 600 {
		t.Errorf("MediaDetails = %+v", got.MediaDetails)
	}
}

func TestRESTMapperMediaDetailsEmptyWhenMetadataUnset(t *testing.T) {
	mapper, _, _, _ := newTestMapper()
	m := domain.Media{ID: 4, URL: "/wp-content/uploads/2024/03/doc.pdf", MimeType: "application/pdf"}

	got, err := mapper.Media(context.Background(), m)
	if err != nil {
		t.Fatalf("Media: %v", err)
	}

	b, err := json.Marshal(got.MediaDetails)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(b) != "{}" {
		t.Errorf("MediaDetails JSON = %s, want {}", b)
	}
}

func TestRESTMapperUserViewContextOmitsPrivateFields(t *testing.T) {
	mapper, _, _, _ := newTestMapper()
	u := domain.User{ID: 5, DisplayName: "Alice", Nicename: "alice", Email: "alice@example.test", URL: "https://alice.example"}

	got, err := mapper.User(context.Background(), u, RESTContextView)
	if err != nil {
		t.Fatalf("User: %v", err)
	}
	view, ok := got.(RESTUser)
	if !ok {
		t.Fatalf("got type %T, want RESTUser", got)
	}
	if view.ID != 5 || view.Name != "Alice" || view.Slug != "alice" || view.Link != "/?author=5" {
		t.Errorf("view = %+v", view)
	}

	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	for _, key := range []string{"email", "url", "roles"} {
		if _, ok := raw[key]; ok {
			t.Errorf("view context JSON has %q key, want absent", key)
		}
	}
}

func TestRESTMapperUserEditContextIncludesRolesFromCapabilities(t *testing.T) {
	mapper, _, _, users := newTestMapper()
	u := domain.User{ID: 5, DisplayName: "Alice", Nicename: "alice", Email: "alice@example.test", URL: "https://alice.example"}
	users.values[5] = map[string]string{
		"wp_capabilities": `a:1:{s:6:"editor";b:1;}`,
	}

	got, err := mapper.User(context.Background(), u, RESTContextEdit)
	if err != nil {
		t.Fatalf("User: %v", err)
	}
	edit, ok := got.(RESTUserEdit)
	if !ok {
		t.Fatalf("got type %T, want RESTUserEdit", got)
	}
	if edit.Email != u.Email {
		t.Errorf("Email = %q, want %q", edit.Email, u.Email)
	}
	if edit.URL != u.URL {
		t.Errorf("URL = %q, want %q", edit.URL, u.URL)
	}
	if len(edit.Roles) != 1 || edit.Roles[0] != "editor" {
		t.Errorf("Roles = %v, want [editor]", edit.Roles)
	}
}

func TestRESTMapperUserEditContextToleratesMissingCapabilities(t *testing.T) {
	mapper, _, _, _ := newTestMapper()
	u := domain.User{ID: 6, DisplayName: "Bob", Nicename: "bob"}

	got, err := mapper.User(context.Background(), u, RESTContextEdit)
	if err != nil {
		t.Fatalf("User: %v", err)
	}
	edit, ok := got.(RESTUserEdit)
	if !ok {
		t.Fatalf("got type %T, want RESTUserEdit", got)
	}
	if edit.Roles == nil || len(edit.Roles) != 0 {
		t.Errorf("Roles = %v, want empty non-nil slice", edit.Roles)
	}
}
