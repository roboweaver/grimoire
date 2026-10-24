package content

import (
	"context"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/sanitize"
)

// ---------------------------------------------------------------------------
// Failing-first (TDD red) tests for content.NavMenuWriteService (task 3.1).
//
// NONE of the symbols these tests reference exist yet (task 3.2 implements
// them): NavMenuWriteService, NewNavMenuWriteService, WithNavMenuContentPolicy,
// MenuInput, MenuItemInput, and the six methods. The file therefore
// compile-fails on those missing symbols, which is the expected red; every
// domain/sanitize/auth symbol it imports already exists (Phase 1 landed the
// domain port + patch/input structs; M10a shipped the Policy).
//
// The service shape under test is the one design.md pins in
// "content.NavMenuWriteService" and its method sketches:
//
//	type NavMenuWriteService struct { w, reader, policy }     // policy never nil
//	type NavMenuWriteOption func(*NavMenuWriteService)
//	func WithNavMenuContentPolicy(p *sanitize.Policy) NavMenuWriteOption
//	func NewNavMenuWriteService(w domain.NavMenuWriter, reader domain.NavMenuRepository, opts ...NavMenuWriteOption) *NavMenuWriteService
//	type MenuInput struct{ Name, Slug string }
//	type MenuItemInput struct{ Label, URL, Type, Object string; ObjectID, ParentID int64; Order int }
//	func (s *NavMenuWriteService) CreateMenu(ctx, principal auth.Principal, in MenuInput) (domain.NavMenu, error)
//	func (s *NavMenuWriteService) CreateItem(ctx, principal auth.Principal, menuID int64, in MenuItemInput) (domain.NavMenu, error)
//
// Every sanitize assertion pins behavior against the REAL sanitize.Policy (the
// oracle), matching mediawriteservice_test.go / userservice_test.go. Pure Go,
// no SQLite. The actor(role, id) helper is defined in writeservices_test.go.

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

// createMenuCall / createItemCall capture what reached the writer, so a test can
// prove the sanitized value (not the raw caller string) is what gets persisted.
type createMenuCall struct {
	name, slug string
}
type createItemCall struct {
	menuID int64
	item   domain.NavMenuItemInput
}

// fakeNavMenuWriter is a recording domain.NavMenuWriter. It serves canned IDs
// and records every create/update/delete so a test can assert both the recorded
// arguments and the "no write happened" (fail-closed) case. ItemBelongsToMenu is
// table-driven: belongs[key{menuID,itemID}] reports membership, defaulting false
// for the cross-menu / nonexistent cases of Req 5.4.
type fakeNavMenuWriter struct {
	nextMenuID int64
	nextItemID int64

	createMenu []createMenuCall
	updateMenu []int64 // ids that reached UpdateMenu
	deleteMenu []int64
	createItem []createItemCall
	updateItem []int64 // itemIDs that reached UpdateItem
	deleteItem []int64

	belongs map[[2]int64]bool

	// notFoundMenu/notFoundItem let a test force domain.ErrNotFound out of the
	// writer for an unknown menu/item id.
	notFoundMenu map[int64]bool
	notFoundItem map[int64]bool
}

func newFakeNavMenuWriter() *fakeNavMenuWriter {
	return &fakeNavMenuWriter{belongs: map[[2]int64]bool{}}
}

func (f *fakeNavMenuWriter) CreateMenu(_ context.Context, name, slug string) (int64, error) {
	f.createMenu = append(f.createMenu, createMenuCall{name: name, slug: slug})
	f.nextMenuID++
	return f.nextMenuID, nil
}

func (f *fakeNavMenuWriter) UpdateMenu(_ context.Context, id int64, _ domain.NavMenuPatch) error {
	if f.notFoundMenu[id] {
		return domain.ErrNotFound
	}
	f.updateMenu = append(f.updateMenu, id)
	return nil
}

func (f *fakeNavMenuWriter) DeleteMenu(_ context.Context, id int64) error {
	if f.notFoundMenu[id] {
		return domain.ErrNotFound
	}
	f.deleteMenu = append(f.deleteMenu, id)
	return nil
}

func (f *fakeNavMenuWriter) CreateItem(_ context.Context, menuID int64, item domain.NavMenuItemInput) (int64, error) {
	if f.notFoundMenu[menuID] {
		return 0, domain.ErrNotFound
	}
	f.createItem = append(f.createItem, createItemCall{menuID: menuID, item: item})
	f.nextItemID++
	return f.nextItemID, nil
}

func (f *fakeNavMenuWriter) UpdateItem(_ context.Context, _ int64, itemID int64, _ domain.NavMenuItemPatch) error {
	if f.notFoundItem[itemID] {
		return domain.ErrNotFound
	}
	f.updateItem = append(f.updateItem, itemID)
	return nil
}

func (f *fakeNavMenuWriter) DeleteItem(_ context.Context, _ int64, itemID int64) error {
	if f.notFoundItem[itemID] {
		return domain.ErrNotFound
	}
	f.deleteItem = append(f.deleteItem, itemID)
	return nil
}

func (f *fakeNavMenuWriter) ItemBelongsToMenu(_ context.Context, menuID, itemID int64) (bool, error) {
	return f.belongs[[2]int64{menuID, itemID}], nil
}

// wrote reports whether ANY create/update reached the writer, for the fail-closed
// assertions ("mutates nothing").
func (f *fakeNavMenuWriter) wrote() bool {
	return len(f.createMenu) != 0 || len(f.updateMenu) != 0 ||
		len(f.createItem) != 0 || len(f.updateItem) != 0
}

// fakeNavMenuReader is a domain.NavMenuRepository reader. It mirrors
// wprepo.NavMenuRepo's read contract exactly where the service depends on it:
//
//   - MenuBySlug returns a zero-value NavMenu{} (ID==0) for a miss and a menu
//     with ID!=0 for a slug that is already taken (NavMenuRepo returns
//     domain.NavMenu{} with a nil error on sql.ErrNoRows, so existing.ID==0 is
//     the correct miss check the slug uniquifier relies on).
//   - MenuByID serves the read-back after a successful write.
type fakeNavMenuReader struct {
	// taken is the set of slugs that already exist (collision hits).
	taken map[string]bool
	// byID is the read-back source for MenuByID.
	byID map[int64]domain.NavMenu

	bySlugCalls []string
}

func newFakeNavMenuReader() *fakeNavMenuReader {
	return &fakeNavMenuReader{taken: map[string]bool{}, byID: map[int64]domain.NavMenu{}}
}

func (r *fakeNavMenuReader) Menus(_ context.Context) ([]domain.NavMenu, error) { return nil, nil }

func (r *fakeNavMenuReader) MenuBySlug(_ context.Context, slug string) (domain.NavMenu, error) {
	r.bySlugCalls = append(r.bySlugCalls, slug)
	if r.taken[slug] {
		return domain.NavMenu{ID: 99, Slug: slug}, nil // hit: ID != 0
	}
	return domain.NavMenu{}, nil // miss: zero value, ID == 0
}

func (r *fakeNavMenuReader) MenuByID(_ context.Context, id int64) (domain.NavMenu, error) {
	if m, ok := r.byID[id]; ok {
		return m, nil
	}
	return domain.NavMenu{ID: id}, nil
}

func (r *fakeNavMenuReader) MenuByLocation(_ context.Context, _, _ string) (domain.NavMenu, error) {
	return domain.NavMenu{}, nil
}

// newNavMenuWriteServiceForTest builds the service over the recording writer and
// the fake reader with the REAL sanitize.Policy wired, matching how the process
// shares one *sanitize.Policy across the write services.
func newNavMenuWriteServiceForTest() (*NavMenuWriteService, *fakeNavMenuWriter, *fakeNavMenuReader) {
	w := newFakeNavMenuWriter()
	r := newFakeNavMenuReader()
	svc := NewNavMenuWriteService(w, r, WithNavMenuContentPolicy(sanitize.New()))
	return svc, w, r
}

// ---------------------------------------------------------------------------
// Sanitization at the write boundary (Req 4.1, 4.2, 4.5)
// ---------------------------------------------------------------------------

// TestNavMenuCreateMenuSanitizesNameToPlainText asserts a menu name containing
// markup is reduced to plain text via sanitize.PostTitle BEFORE it reaches the
// writer: the recorded CreateMenu name matches the real policy oracle at
// (PostTitle, For(principal)) and carries no markup delimiters (Req 4.2).
func TestNavMenuCreateMenuSanitizesNameToPlainText(t *testing.T) {
	svc, w, _ := newNavMenuWriteServiceForTest()
	p := actor(auth.RoleAuthor, 1)

	const nameIn = `<script>alert(1)</script><b>Primary</b> Menu`
	want, err := sanitize.New().Sanitize(sanitize.PostTitle, sanitize.For(p), nameIn)
	if err != nil {
		t.Fatalf("oracle name: %v", err)
	}

	if _, err := svc.CreateMenu(context.Background(), p, MenuInput{Name: nameIn}); err != nil {
		t.Fatalf("CreateMenu: %v", err)
	}
	if len(w.createMenu) != 1 {
		t.Fatalf("CreateMenu should write once: createMenu = %+v", w.createMenu)
	}
	got := w.createMenu[0].name
	if strings.ContainsAny(got, "<>") {
		t.Errorf("persisted name not reduced to plain text: %q", got)
	}
	if got != want {
		t.Errorf("persisted name = %q, want policy oracle %q (PostTitle, For(principal))", got, want)
	}
}

// TestNavMenuCreateItemSanitizesLabelToPlainText asserts an item label
// containing markup is reduced to plain text via sanitize.PostTitle before it
// reaches the writer as the item's Label -> post_title (Req 4.1). The item is a
// top-level custom item with an acceptable URL so only the label sanitization is
// under test here.
func TestNavMenuCreateItemSanitizesLabelToPlainText(t *testing.T) {
	svc, w, _ := newNavMenuWriteServiceForTest()
	p := actor(auth.RoleAuthor, 1)

	const labelIn = `<b>x</b> <script>steal()</script> Home`
	want, err := sanitize.New().Sanitize(sanitize.PostTitle, sanitize.For(p), labelIn)
	if err != nil {
		t.Fatalf("oracle label: %v", err)
	}

	if _, err := svc.CreateItem(context.Background(), p, 1, MenuItemInput{
		Label: labelIn,
		Type:  "custom",
		URL:   "https://example.com/home",
	}); err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	if len(w.createItem) != 1 {
		t.Fatalf("CreateItem should write once: createItem = %+v", w.createItem)
	}
	got := w.createItem[0].item.Label
	if strings.ContainsAny(got, "<>") {
		t.Errorf("persisted label not reduced to plain text: %q", got)
	}
	if got != want {
		t.Errorf("persisted label = %q, want policy oracle %q (PostTitle, For(principal))", got, want)
	}
}

// TestNavMenuSanitizeWriterArgPrincipalDerived asserts the Policy's writer
// argument is built from the principal (Req 4.5): a present principal uses
// sanitize.For(principal) and the no-principal case uses sanitize.Anonymous().
//
// The present-principal branch uses a tier-B author (RoleAuthor, who lacks
// unfiltered_html): at tier B the PostTitle reduction strips markup, so the
// observable contract is "the sanitized value reaching the writer equals the
// real-Policy oracle at (PostTitle, For(author)), and carries no markup". The
// no-principal branch uses sanitize.Anonymous() as the writer and matches that
// oracle. Each branch pins the writer argument the service derived from the
// principal, not any claim about tier-independence of PostTitle.
func TestNavMenuSanitizeWriterArgPrincipalDerived(t *testing.T) {
	const nameIn = `<em>Keep</em> <script>x()</script> Me`

	t.Run("present principal uses For(principal)", func(t *testing.T) {
		svc, w, _ := newNavMenuWriteServiceForTest()
		p := actor(auth.RoleAuthor, 1) // lacks unfiltered_html -> tier B
		want, err := sanitize.New().Sanitize(sanitize.PostTitle, sanitize.For(p), nameIn)
		if err != nil {
			t.Fatalf("oracle For(author): %v", err)
		}
		if _, err := svc.CreateMenu(context.Background(), p, MenuInput{Name: nameIn}); err != nil {
			t.Fatalf("CreateMenu: %v", err)
		}
		if len(w.createMenu) != 1 {
			t.Fatalf("CreateMenu should write once: %+v", w.createMenu)
		}
		got := w.createMenu[0].name
		if strings.ContainsAny(got, "<>") {
			t.Errorf("author (tier B) name NOT reduced to plain text: %q", got)
		}
		if got != want {
			t.Errorf("name = %q, want For(author) oracle %q", got, want)
		}
	})

	t.Run("no principal uses Anonymous()", func(t *testing.T) {
		svc, w, _ := newNavMenuWriteServiceForTest()
		var none auth.Principal // zero-value: no principal present
		want, err := sanitize.New().Sanitize(sanitize.PostTitle, sanitize.Anonymous(), nameIn)
		if err != nil {
			t.Fatalf("oracle Anonymous(): %v", err)
		}
		if _, err := svc.CreateMenu(context.Background(), none, MenuInput{Name: nameIn}); err != nil {
			t.Fatalf("CreateMenu: %v", err)
		}
		if len(w.createMenu) != 1 {
			t.Fatalf("CreateMenu should write once: %+v", w.createMenu)
		}
		got := w.createMenu[0].name
		if strings.ContainsAny(got, "<>") {
			t.Errorf("anonymous name NOT reduced to plain text: %q", got)
		}
		if got != want {
			t.Errorf("name = %q, want Anonymous() oracle %q", got, want)
		}
	})
}

// ---------------------------------------------------------------------------
// Custom URL validation (Req 4.3, 4.6, 9.3)
// ---------------------------------------------------------------------------

// TestNavMenuCreateItemRejectsDangerousCustomURL asserts a custom item whose URL
// carries a non-acceptable scheme (javascript:, data:) is REJECTED: CreateItem
// returns a non-nil error and NOTHING reaches the writer (fail closed). The
// label is clean so only the URL is the rejection cause.
func TestNavMenuCreateItemRejectsDangerousCustomURL(t *testing.T) {
	for _, badURL := range []string{
		"javascript:alert(1)",
		"data:text/html,<script>alert(1)</script>",
	} {
		t.Run(badURL, func(t *testing.T) {
			svc, w, _ := newNavMenuWriteServiceForTest()
			_, err := svc.CreateItem(context.Background(), actor(auth.RoleAdministrator, 1), 1, MenuItemInput{
				Label: "Danger",
				Type:  "custom",
				URL:   badURL,
			})
			if err == nil {
				t.Fatalf("custom URL %q: err = nil, want rejection", badURL)
			}
			if w.wrote() {
				t.Fatalf("rejected URL must not write: createItem = %+v", w.createItem)
			}
		})
	}
}

// TestNavMenuCreateItemAcceptsSafeCustomURL asserts the acceptable schemes and a
// relative path are accepted and persisted to the writer (escaped): http(s),
// mailto, tel, ftp, and a relative /path all reach CreateItem with the URL
// stored on the item (Req 4.3).
func TestNavMenuCreateItemAcceptsSafeCustomURL(t *testing.T) {
	for _, okURL := range []string{
		"http://example.com/a",
		"https://example.com/b",
		"mailto:a@b.c",
		"tel:+1",
		"ftp://files.example.com/x",
		"/relative/path",
	} {
		t.Run(okURL, func(t *testing.T) {
			svc, w, _ := newNavMenuWriteServiceForTest()
			if _, err := svc.CreateItem(context.Background(), actor(auth.RoleAdministrator, 1), 1, MenuItemInput{
				Label: "Link",
				Type:  "custom",
				URL:   okURL,
			}); err != nil {
				t.Fatalf("custom URL %q: unexpected err %v", okURL, err)
			}
			if len(w.createItem) != 1 {
				t.Fatalf("accepted URL should write once: createItem = %+v", w.createItem)
			}
			if got := w.createItem[0].item.URL; got == "" {
				t.Errorf("accepted URL %q not persisted to item (got empty)", okURL)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Item type validation (Req 6.2, open Q3)
// ---------------------------------------------------------------------------

// TestNavMenuCreateItemRejectsUnknownType asserts an item type outside
// {custom, post_type, taxonomy} is rejected with no writer call (Req 6.2).
func TestNavMenuCreateItemRejectsUnknownType(t *testing.T) {
	svc, w, _ := newNavMenuWriteServiceForTest()
	_, err := svc.CreateItem(context.Background(), actor(auth.RoleAdministrator, 1), 1, MenuItemInput{
		Label: "Bad",
		Type:  "widget", // not in the accepted set
	})
	if err == nil {
		t.Fatal("unknown item type: err = nil, want rejection")
	}
	if w.wrote() {
		t.Fatalf("unknown type must not write: createItem = %+v", w.createItem)
	}
}

// TestNavMenuCreateItemAcceptsKnownTypes asserts each accepted type is allowed
// through to the writer. post_type/taxonomy carry an object reference; custom
// carries a URL.
func TestNavMenuCreateItemAcceptsKnownTypes(t *testing.T) {
	cases := []MenuItemInput{
		{Label: "C", Type: "custom", URL: "https://example.com"},
		{Label: "P", Type: "post_type", Object: "page", ObjectID: 10},
		{Label: "T", Type: "taxonomy", Object: "category", ObjectID: 5},
	}
	for _, in := range cases {
		t.Run(in.Type, func(t *testing.T) {
			svc, w, _ := newNavMenuWriteServiceForTest()
			if _, err := svc.CreateItem(context.Background(), actor(auth.RoleAdministrator, 1), 1, in); err != nil {
				t.Fatalf("type %q: unexpected err %v", in.Type, err)
			}
			if len(w.createItem) != 1 {
				t.Fatalf("type %q should write once: createItem = %+v", in.Type, w.createItem)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Parent validation / nesting (Req 5.3, 5.4)
// ---------------------------------------------------------------------------

// TestNavMenuCreateItemRejectsCrossMenuParent asserts a non-zero ParentID that
// is NOT an item of the same menu (ItemBelongsToMenu reports false) is rejected
// with no writer mutation (Req 5.4).
func TestNavMenuCreateItemRejectsCrossMenuParent(t *testing.T) {
	svc, w, _ := newNavMenuWriteServiceForTest()
	// belongs is empty, so ItemBelongsToMenu(menu=1, parent=42) is false.
	_, err := svc.CreateItem(context.Background(), actor(auth.RoleAdministrator, 1), 1, MenuItemInput{
		Label:    "Child",
		Type:     "custom",
		URL:      "https://example.com",
		ParentID: 42,
	})
	if err == nil {
		t.Fatal("cross-menu parent: err = nil, want rejection")
	}
	if w.wrote() {
		t.Fatalf("bad parent must not write: createItem = %+v", w.createItem)
	}
}

// TestNavMenuCreateItemTopLevelParentZeroSkipsBelongsCheck asserts ParentID == 0
// is accepted as a top-level item and the belongs-check is skipped (Req 5.3):
// even with an empty belongs map (which would reject any non-zero parent), a
// zero parent reaches the writer.
func TestNavMenuCreateItemTopLevelParentZeroSkipsBelongsCheck(t *testing.T) {
	svc, w, _ := newNavMenuWriteServiceForTest()
	if _, err := svc.CreateItem(context.Background(), actor(auth.RoleAdministrator, 1), 1, MenuItemInput{
		Label:    "Top",
		Type:     "custom",
		URL:      "https://example.com",
		ParentID: 0,
	}); err != nil {
		t.Fatalf("top-level item: unexpected err %v", err)
	}
	if len(w.createItem) != 1 {
		t.Fatalf("top-level item should write once: createItem = %+v", w.createItem)
	}
}

// TestNavMenuCreateItemAcceptsValidSameMenuParent asserts a non-zero ParentID
// that DOES belong to the same menu is accepted and persisted (Req 5.2, 5.4).
func TestNavMenuCreateItemAcceptsValidSameMenuParent(t *testing.T) {
	svc, w, _ := newNavMenuWriteServiceForTest()
	w.belongs[[2]int64{1, 7}] = true // item 7 belongs to menu 1
	if _, err := svc.CreateItem(context.Background(), actor(auth.RoleAdministrator, 1), 1, MenuItemInput{
		Label:    "Child",
		Type:     "custom",
		URL:      "https://example.com",
		ParentID: 7,
	}); err != nil {
		t.Fatalf("valid same-menu parent: unexpected err %v", err)
	}
	if len(w.createItem) != 1 {
		t.Fatalf("valid parent should write once: createItem = %+v", w.createItem)
	}
	if got := w.createItem[0].item.ParentID; got != 7 {
		t.Errorf("persisted ParentID = %d, want 7", got)
	}
}

// ---------------------------------------------------------------------------
// Slug resolution (resolved open question 2)
// ---------------------------------------------------------------------------

// TestNavMenuResolveSlugDerivesFromName asserts an omitted slug is derived from
// the name with WordPress sanitize_title semantics ("My Menu!" -> "my-menu").
func TestNavMenuResolveSlugDerivesFromName(t *testing.T) {
	svc, w, _ := newNavMenuWriteServiceForTest()
	if _, err := svc.CreateMenu(context.Background(), actor(auth.RoleAdministrator, 1), MenuInput{
		Name: "My Menu!",
	}); err != nil {
		t.Fatalf("CreateMenu: %v", err)
	}
	if len(w.createMenu) != 1 {
		t.Fatalf("CreateMenu should write once: %+v", w.createMenu)
	}
	if got := w.createMenu[0].slug; got != "my-menu" {
		t.Errorf("derived slug = %q, want %q", got, "my-menu")
	}
}

// TestNavMenuResolveSlugNormalizesExplicitSlug asserts an explicit slug is
// normalized through the same sanitize_title semantics.
func TestNavMenuResolveSlugNormalizesExplicitSlug(t *testing.T) {
	svc, w, _ := newNavMenuWriteServiceForTest()
	if _, err := svc.CreateMenu(context.Background(), actor(auth.RoleAdministrator, 1), MenuInput{
		Name: "Primary",
		Slug: "My Custom Slug!",
	}); err != nil {
		t.Fatalf("CreateMenu: %v", err)
	}
	if got := w.createMenu[0].slug; got != "my-custom-slug" {
		t.Errorf("normalized slug = %q, want %q", got, "my-custom-slug")
	}
}

// TestNavMenuResolveSlugAppendsSuffixOnCollision asserts a collision on the base
// slug (reader.MenuBySlug returns a hit) appends -2, and a second collision -3.
func TestNavMenuResolveSlugAppendsSuffixOnCollision(t *testing.T) {
	t.Run("single collision appends -2", func(t *testing.T) {
		svc, w, r := newNavMenuWriteServiceForTest()
		r.taken["primary"] = true // base is taken; "primary-2" is free
		if _, err := svc.CreateMenu(context.Background(), actor(auth.RoleAdministrator, 1), MenuInput{
			Name: "Primary",
		}); err != nil {
			t.Fatalf("CreateMenu: %v", err)
		}
		if got := w.createMenu[0].slug; got != "primary-2" {
			t.Errorf("collision slug = %q, want %q", got, "primary-2")
		}
	})

	t.Run("double collision appends -3", func(t *testing.T) {
		svc, w, r := newNavMenuWriteServiceForTest()
		r.taken["primary"] = true
		r.taken["primary-2"] = true // both taken; "primary-3" is free
		if _, err := svc.CreateMenu(context.Background(), actor(auth.RoleAdministrator, 1), MenuInput{
			Name: "Primary",
		}); err != nil {
			t.Fatalf("CreateMenu: %v", err)
		}
		if got := w.createMenu[0].slug; got != "primary-3" {
			t.Errorf("double-collision slug = %q, want %q", got, "primary-3")
		}
	})
}

// ---------------------------------------------------------------------------
// Fail closed: every policy/validation error returns before the writer
// (Req 9.1, 9.3) — spanning the URL, type, and parent rejection paths.
// ---------------------------------------------------------------------------

// TestNavMenuCreateItemFailClosedMutatesNothing asserts that for each rejection
// cause the writer records NO create and NO ItemBelongsToMenu-driven mutation,
// so a rejected request leaves the stored state unchanged.
func TestNavMenuCreateItemFailClosedMutatesNothing(t *testing.T) {
	cases := map[string]MenuItemInput{
		"bad url":    {Label: "x", Type: "custom", URL: "javascript:alert(1)"},
		"bad type":   {Label: "x", Type: "nope"},
		"bad parent": {Label: "x", Type: "custom", URL: "https://ok", ParentID: 123},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			svc, w, _ := newNavMenuWriteServiceForTest()
			if _, err := svc.CreateItem(context.Background(), actor(auth.RoleAdministrator, 1), 1, in); err == nil {
				t.Fatalf("%s: err = nil, want rejection", name)
			}
			if w.wrote() {
				t.Fatalf("%s: fail-closed must not write: createItem = %+v", name, w.createItem)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Policy wiring (Req 3.4)
// ---------------------------------------------------------------------------

// TestNavMenuWithContentPolicyOverridesDefault asserts WithNavMenuContentPolicy
// wires the supplied *sanitize.Policy, overriding the constructor default, so
// the process-wide shared policy reaches the service.
func TestNavMenuWithContentPolicyOverridesDefault(t *testing.T) {
	pol := sanitize.New()
	svc := NewNavMenuWriteService(newFakeNavMenuWriter(), newFakeNavMenuReader(), WithNavMenuContentPolicy(pol))
	if svc.policy != pol {
		t.Fatalf("WithNavMenuContentPolicy did not override default: policy = %p, want %p", svc.policy, pol)
	}
}

// TestNavMenuNewServiceDefaultsPolicyNeverNil asserts a service built with NO
// option still holds a non-nil sanitize.New() Policy (fail closed, Req 3.4) and
// does not nil-panic when it sanitizes a name on CreateMenu.
func TestNavMenuNewServiceDefaultsPolicyNeverNil(t *testing.T) {
	w := newFakeNavMenuWriter()
	svc := NewNavMenuWriteService(w, newFakeNavMenuReader())
	if svc.policy == nil {
		t.Fatal("NewNavMenuWriteService default policy is nil; want sanitize.New() (never nil)")
	}
	// Must not nil-panic sanitizing with the defaulted policy.
	if _, err := svc.CreateMenu(context.Background(), actor(auth.RoleAdministrator, 1), MenuInput{
		Name: "<b>Hi</b>",
	}); err != nil {
		t.Fatalf("CreateMenu with defaulted policy: %v", err)
	}
	if len(w.createMenu) != 1 {
		t.Fatalf("CreateMenu should write once with defaulted policy: %+v", w.createMenu)
	}
}
