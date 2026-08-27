// API response shapes mirror internal/web/adminapi.go. All endpoints are GET,
// JSON, same-origin, cookie-authenticated under /admin/api.

export interface SessionInfo {
  id: number;
  login: string;
  displayName: string;
  roles: string[];
  capabilities: string[];
  csrfToken: string;
}

export interface Stats {
  posts: { published: number; draft: number };
  pages: number;
  categories: number;
  users: number;
}

export interface PostListItem {
  id: number;
  title: string;
  slug: string;
  type: string;
  status: string;
  author: number;
  date: string;
}

export interface AuthorOption {
  id: number;
  displayName: string;
}

export interface PostList {
  items: PostListItem[];
  page: number;
  perPage: number;
  total: number;
  totalPages: number;
}

// TermSummary mirrors internal/web.termSummary (Req 2.1/2.4, 4.1's terms map
// values).
export interface TermSummary {
  id: number;
  name: string;
  slug: string;
}

export interface PostDetail {
  id: number;
  title: string;
  slug: string;
  type: string;
  status: string;
  author: number;
  date: string;
  // modified backs Req 3's optimistic-concurrency check: it must be sent
  // back unchanged as the update request's `modified` field.
  modified: string;
  excerpt: string;
  content: string;
  commentStatus: string;
  // terms is keyed by taxonomy ("category", "post_tag"); every known
  // taxonomy is always present with a (possibly empty) array (Req 4.1).
  terms: Record<string, TermSummary[]>;
  // partial reports per-taxonomy term-assignment failures that happened
  // after the post write itself already succeeded (Req 2.2); absent when
  // every taxonomy in the request applied cleanly.
  partial?: Record<string, string>;
}

// PostWriteInput is the create/update request body
// (internal/web.postWriteRequest). `modified` is required on update (the
// optimistic-concurrency token, Req 3) and omitted on create; `termIds` maps
// taxonomy -> the full desired set of term IDs for that taxonomy (Req 2.2).
export interface PostWriteInput {
  title: string;
  content: string;
  excerpt: string;
  slug: string;
  status: string;
  type: string;
  date?: string;
  commentStatus: string;
  modified?: string;
  termIds?: Record<string, number[]>;
}

// TermDetail mirrors internal/web.termDetailResponse (Req 2.3/2.4).
export interface TermDetail {
  id: number;
  name: string;
  slug: string;
  taxonomy?: string;
}

export interface TermWriteInput {
  name: string;
  slug: string;
  taxonomy?: string;
}

export interface TermList {
  items: TermSummary[];
}

export interface ApiErrorBody {
  error: { code: string; message: string };
}

export interface Comment {
  id: number;
  postId: number;
  postTitle: string;
  author: string;
  authorEmail: string;
  authorURL: string;
  content: string;
  excerpt: string;
  status: string;
  date: string;
}

export interface CommentList {
  items: Comment[];
  page: number;
  perPage: number;
  total: number;
  totalPages: number;
}

export interface MediaItem {
  id: number;
  title: string;
  filename: string;
  url: string;
  mimeType: string;
  date: string;
  parentId: number;
}

export interface MediaList {
  items: MediaItem[];
  page: number;
  perPage: number;
  total: number;
  totalPages: number;
}

export interface NavMenuItem {
  id: number;
  label: string;
  url: string;
  type: string;
  object: string;
  objectId: number;
  parentId: number;
  order: number;
  children: NavMenuItem[];
}

export interface NavMenu {
  id: number;
  name: string;
  slug: string;
  items: NavMenuItem[];
}

// RevisionSummary mirrors internal/web.revisionSummary (Req 2.1): no content
// body, newest-first list entries for the Revisions panel.
export interface RevisionSummary {
  id: number;
  author: number;
  modified: string;
}

// RevisionDetail mirrors internal/web.revisionDetail (Req 2.2): the full
// title/content/excerpt body for a single revision, used for the
// restore-preview diff and the restore action's response shape (a PostDetail,
// reused as-is).
export interface RevisionDetail {
  id: number;
  title: string;
  content: string;
  excerpt: string;
  modified: string;
}

// AutosaveDetail mirrors internal/web.autosaveResponse (Req 3.5/3.6): both
// GET and POST .../autosave return this same title/content/excerpt/modified
// shape.
export interface AutosaveDetail {
  title: string;
  content: string;
  excerpt: string;
  modified: string;
}

// AutosaveWriteInput is the JSON body POST .../autosave accepts
// (internal/web.autosaveWriteRequest, Req 3.1).
export interface AutosaveWriteInput {
  title: string;
  content: string;
  excerpt: string;
}
