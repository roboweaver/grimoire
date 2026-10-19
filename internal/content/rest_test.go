package content

import (
	"context"
	"encoding/json"
	"strings"
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
	// newTestMapper carries a flat structure, so the author archive path is
	// "/author/alice" with no trailing slash (Req 7.5, 10.3). It replaces the
	// "/?author=5" fallback M9a kept while no author route existed.
	if view.ID != 5 || view.Name != "Alice" || view.Slug != "alice" || view.Link != "/author/alice" {
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

// sampleMedia is an attachment whose editable-metadata fields are all
// populated, so the mapper's emission of caption/alt_text/description can be
// asserted independently of the create-time filename-derived title.
func sampleMedia() domain.Media {
	return domain.Media{
		ID:          3,
		Title:       "A Photo",
		Filename:    "2024/03/a-photo.jpg",
		URL:         "/wp-content/uploads/2024/03/a-photo.jpg",
		MimeType:    "image/jpeg",
		Date:        time.Date(2024, 3, 5, 9, 0, 0, 0, time.UTC),
		ParentID:    42,
		Slug:        "a-photo",
		AuthorID:    7,
		Caption:     "A short caption",
		AltText:     "Alt text for the photo",
		Description: "A longer description of the photo.",
	}
}

// TestRESTMapperMediaEmitsCaptionAltTextDescription pins the three M10.B
// read-side fields (Finding 3, Req 10.3): caption and description are the
// WordPress {"rendered": "..."} sub-object shape (RESTRendered), alt_text is a
// bare string, each populated straight from domain.Media.
func TestRESTMapperMediaEmitsCaptionAltTextDescription(t *testing.T) {
	mapper, _, _, _ := newTestMapper()
	m := sampleMedia()

	got, err := mapper.Media(context.Background(), m)
	if err != nil {
		t.Fatalf("Media: %v", err)
	}

	if got.Caption.Rendered != m.Caption {
		t.Errorf("Caption.Rendered = %q, want %q", got.Caption.Rendered, m.Caption)
	}
	if got.Description.Rendered != m.Description {
		t.Errorf("Description.Rendered = %q, want %q", got.Description.Rendered, m.Description)
	}
	if got.AltText != m.AltText {
		t.Errorf("AltText = %q, want %q", got.AltText, m.AltText)
	}
}

// TestRESTMapperMediaNewFieldsJSONShape pins the JSON tags and shapes so task
// 5.2 matches WordPress: "caption"/"description" are {"rendered": "..."}
// sub-objects, "alt_text" is a bare string.
func TestRESTMapperMediaNewFieldsJSONShape(t *testing.T) {
	mapper, _, _, _ := newTestMapper()

	got, err := mapper.Media(context.Background(), sampleMedia())
	if err != nil {
		t.Fatalf("Media: %v", err)
	}

	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	caption, ok := raw["caption"].(map[string]any)
	if !ok {
		t.Fatalf(`json "caption" = %v, want {"rendered": ...} object`, raw["caption"])
	}
	if caption["rendered"] != "A short caption" {
		t.Errorf(`json caption.rendered = %v, want "A short caption"`, caption["rendered"])
	}

	description, ok := raw["description"].(map[string]any)
	if !ok {
		t.Fatalf(`json "description" = %v, want {"rendered": ...} object`, raw["description"])
	}
	if description["rendered"] != "A longer description of the photo." {
		t.Errorf(`json description.rendered = %v, want the description text`, description["rendered"])
	}

	altText, ok := raw["alt_text"].(string)
	if !ok {
		t.Fatalf(`json "alt_text" = %v, want a bare string`, raw["alt_text"])
	}
	if altText != "Alt text for the photo" {
		t.Errorf(`json alt_text = %q, want "Alt text for the photo"`, altText)
	}
}

// TestRESTMapperMediaNewFieldsStableWhenEmpty asserts that an attachment with
// empty caption/alt_text/description still emits the keys (shape stable): the
// RESTRendered fields are present as {"rendered": ""} and alt_text is a bare
// "" string, never absent (Req 10.3 shape stability).
func TestRESTMapperMediaNewFieldsStableWhenEmpty(t *testing.T) {
	mapper, _, _, _ := newTestMapper()
	m := domain.Media{
		ID:       4,
		URL:      "/wp-content/uploads/2024/03/doc.pdf",
		MimeType: "application/pdf",
	}

	got, err := mapper.Media(context.Background(), m)
	if err != nil {
		t.Fatalf("Media: %v", err)
	}

	if got.Caption.Rendered != "" {
		t.Errorf("Caption.Rendered = %q, want empty", got.Caption.Rendered)
	}
	if got.Description.Rendered != "" {
		t.Errorf("Description.Rendered = %q, want empty", got.Description.Rendered)
	}
	if got.AltText != "" {
		t.Errorf("AltText = %q, want empty", got.AltText)
	}

	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	for _, key := range []string{"caption", "alt_text", "description"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("json missing %q key, want present (shape stable)", key)
		}
	}
	if cap, ok := raw["caption"].(map[string]any); !ok {
		t.Errorf(`json "caption" = %v, want {"rendered": ""} object`, raw["caption"])
	} else if cap["rendered"] != "" {
		t.Errorf(`json caption.rendered = %v, want ""`, cap["rendered"])
	}
	if desc, ok := raw["description"].(map[string]any); !ok {
		t.Errorf(`json "description" = %v, want {"rendered": ""} object`, raw["description"])
	} else if desc["rendered"] != "" {
		t.Errorf(`json description.rendered = %v, want ""`, desc["rendered"])
	}
	if alt, ok := raw["alt_text"].(string); !ok || alt != "" {
		t.Errorf(`json "alt_text" = %v, want bare "" string`, raw["alt_text"])
	}
}

// TestRESTMapperMediaExistingFieldsUnchanged asserts the pre-10.B emitted
// fields are untouched by the additive change (Req 10.1) and that no
// content.rendered field is introduced for media (Req 10.2): media has no
// content field at all.
func TestRESTMapperMediaExistingFieldsUnchanged(t *testing.T) {
	mapper, _, meta, _ := newTestMapper()
	meta.attach[3] = `a:2:{s:5:"width";i:800;s:6:"height";i:600;}`
	m := sampleMedia()

	got, err := mapper.Media(context.Background(), m)
	if err != nil {
		t.Fatalf("Media: %v", err)
	}

	if got.ID != 3 {
		t.Errorf("ID = %d, want 3", got.ID)
	}
	if got.Date != "2024-03-05T09:00:00" {
		t.Errorf("Date = %q", got.Date)
	}
	if got.Slug != "a-photo" {
		t.Errorf("Slug = %q", got.Slug)
	}
	if got.Type != "attachment" {
		t.Errorf("Type = %q, want attachment", got.Type)
	}
	if got.Link != m.URL {
		t.Errorf("Link = %q, want %q", got.Link, m.URL)
	}
	if got.Title.Rendered != "A Photo" {
		t.Errorf("Title.Rendered = %q, want %q", got.Title.Rendered, m.Title)
	}
	if got.Author != 7 {
		t.Errorf("Author = %d, want 7", got.Author)
	}
	if got.MimeType != "image/jpeg" {
		t.Errorf("MimeType = %q", got.MimeType)
	}
	if got.SourceURL != m.URL {
		t.Errorf("SourceURL = %q, want %q", got.SourceURL, m.URL)
	}
	if got.Post != 42 {
		t.Errorf("Post = %d, want 42", got.Post)
	}
	if got.MediaDetails.Width != 800 || got.MediaDetails.Height != 600 {
		t.Errorf("MediaDetails = %+v", got.MediaDetails)
	}

	// Req 10.2: media carries no content field, so content.rendered must not
	// appear in the emitted JSON.
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, ok := raw["content"]; ok {
		t.Errorf(`json has "content" key, want absent for media (Req 10.2)`)
	}
}

// --- Task 5.1 (RED): The Transform wired into restCommon ---------------------
//
// These tests assert that (*RESTMapper).Post and (*RESTMapper).Page emit a
// TRANSFORMED content.rendered — block comments stripped and wp-image-{id}
// <img> enhanced — once task 5.2 routes restCommon's Content.Rendered through
// m.renderContent(ctx, p.Content). RIGHT NOW restCommon still assigns the bare
// p.Content, so the transform tests below (and the Content.Rendered part of the
// sibling-fields test) FAIL for the right reason: the comment is still present
// and the <img> is not enhanced. The byte-identical and sibling-field
// invariants are guards that must stay green across the 5.2 change (Req 4.4,
// 4.5). They reuse the parseImg/imgAttr/seededMediaBlob helpers from
// rest_render_test.go (same package).

// blockImgPost builds a post whose stored Content carries a Gutenberg block
// comment wrapping a media-library <img class="wp-image-201">. After The
// Transform the comment is gone and the <img> is enhanced; before 5.2 the raw
// Content passes straight through.
func blockImgPost() domain.Post {
	p := samplePost()
	p.Content = `<!-- wp:image --><p>cap</p>` +
		`<img class="wp-image-201" src="https://x/wp-content/uploads/2024/03/photo.jpg">` +
		`<!-- /wp:image -->`
	return p
}

// TestRESTMapperPostContentRenderedTransformed asserts Post emits a transformed
// content.rendered: the Gutenberg block comment is stripped and the
// wp-image-{id} <img> is enhanced with loading="lazy", decoding="async", and a
// non-empty srcset (Req 1.1). FAILS pre-5.2 because restCommon returns the raw
// p.Content (comment present, <img> not enhanced).
func TestRESTMapperPostContentRenderedTransformed(t *testing.T) {
	mapper, _, meta, _ := newTestMapper()
	meta.attach[201] = seededMediaBlob(t)

	got, err := mapper.Post(context.Background(), blockImgPost())
	if err != nil {
		t.Fatalf("Post: %v", err)
	}

	rendered := got.Content.Rendered
	if strings.Contains(rendered, "wp:image") {
		t.Errorf("block comment not stripped from content.rendered: %q", rendered)
	}
	img := parseImg(t, rendered)
	if img == nil {
		t.Fatalf("no <img> in content.rendered: %q", rendered)
	}
	if v, ok := imgAttr(img, "loading"); !ok || v != "lazy" {
		t.Errorf("img loading = %q present=%t, want lazy (rendered=%q)", v, ok, rendered)
	}
	if v, ok := imgAttr(img, "decoding"); !ok || v != "async" {
		t.Errorf("img decoding = %q present=%t, want async (rendered=%q)", v, ok, rendered)
	}
	if v, ok := imgAttr(img, "srcset"); !ok || v == "" {
		t.Errorf("img srcset = %q present=%t, want non-empty (rendered=%q)", v, ok, rendered)
	}
}

// TestRESTMapperPageContentRenderedTransformed proves Page — which also flows
// through restCommon — gets the same transform as Post (Req 1.1). FAILS pre-5.2
// for the same reason.
func TestRESTMapperPageContentRenderedTransformed(t *testing.T) {
	mapper, _, meta, _ := newTestMapper()
	meta.attach[201] = seededMediaBlob(t)
	p := blockImgPost()
	p.Type = "page"
	p.Slug = "about"

	got, err := mapper.Page(context.Background(), p)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}

	rendered := got.Content.Rendered
	if strings.Contains(rendered, "wp:image") {
		t.Errorf("block comment not stripped from page content.rendered: %q", rendered)
	}
	img := parseImg(t, rendered)
	if img == nil {
		t.Fatalf("no <img> in page content.rendered: %q", rendered)
	}
	if v, ok := imgAttr(img, "loading"); !ok || v != "lazy" {
		t.Errorf("img loading = %q present=%t, want lazy (rendered=%q)", v, ok, rendered)
	}
	if v, ok := imgAttr(img, "decoding"); !ok || v != "async" {
		t.Errorf("img decoding = %q present=%t, want async (rendered=%q)", v, ok, rendered)
	}
	if v, ok := imgAttr(img, "srcset"); !ok || v == "" {
		t.Errorf("img srcset = %q present=%t, want non-empty (rendered=%q)", v, ok, rendered)
	}
}

// TestRESTMapperPostContentByteIdenticalWhenNoBlocksOrImages asserts Req 4.5:
// a comment-free, image-free post's content.rendered is byte-identical to its
// stored Content. This is a guard — it is green both before 5.2 (raw
// passthrough == input) and after (renderContent short-circuits the parser for
// image-free content) — and locks the invariant across the 5.2 change.
func TestRESTMapperPostContentByteIdenticalWhenNoBlocksOrImages(t *testing.T) {
	mapper, _, _, _ := newTestMapper()
	p := samplePost()
	p.Content = "<p>plain paragraph</p>"

	got, err := mapper.Post(context.Background(), p)
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if got.Content.Rendered != p.Content {
		t.Errorf("content.rendered = %q, want byte-identical %q", got.Content.Rendered, p.Content)
	}
}

// TestRESTMapperPostSiblingFieldsUnchangedByTransform asserts Req 4.4: the
// transform touches ONLY Content.Rendered — Content.Protected and every sibling
// field emitted by restCommon are a function of the domain.Post, not of the
// Content transform, so they are identical to what the pre-10.D mapper produced
// for samplePost regardless of what Content carries. The block-comment+img post
// is used so the Content transform is active while the siblings are asserted
// against samplePost's expected values.
func TestRESTMapperPostSiblingFieldsUnchangedByTransform(t *testing.T) {
	mapper, terms, meta, _ := newTestMapper()
	terms.byPost[42] = map[string][]int64{
		TaxonomyCategory: {3, 1},
		taxonomyPostTag:  {9},
	}
	meta.featured[42] = 55
	meta.attach[201] = seededMediaBlob(t)

	got, err := mapper.Post(context.Background(), blockImgPost())
	if err != nil {
		t.Fatalf("Post: %v", err)
	}

	// Sibling fields match the TestRESTMapperPostFieldsAndFormats expectations
	// for samplePost — the Content transform must not perturb any of them.
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
	// Excerpt is derived from Content (block comments stripped), but is NOT
	// routed through The Transform — it is restCommon's Excerpt(p) and is
	// unchanged by this milestone (Req 4.3). Asserting it is present and not
	// perturbed into the img-enhanced form guards that boundary.
	if strings.Contains(got.Excerpt.Rendered, "loading=") {
		t.Errorf("excerpt.rendered was routed through The Transform: %q", got.Excerpt.Rendered)
	}

	// Protected is p.Password != "", independent of Content. false here.
	if got.Content.Protected {
		t.Errorf("Content.Protected = true, want false (no password)")
	}

	// Sub-case: a password-protected post still reports Protected == true, and
	// the Content transform does not disturb that flag.
	pp := blockImgPost()
	pp.Password = "secret"
	gotPP, err := mapper.Post(context.Background(), pp)
	if err != nil {
		t.Fatalf("Post (password): %v", err)
	}
	if !gotPP.Content.Protected {
		t.Errorf("Content.Protected = false, want true (password set)")
	}
}
