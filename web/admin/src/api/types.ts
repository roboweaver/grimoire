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

export interface PostList {
  items: PostListItem[];
  page: number;
  perPage: number;
  total: number;
  totalPages: number;
}

export interface PostDetail {
  id: number;
  title: string;
  slug: string;
  type: string;
  status: string;
  author: number;
  date: string;
  excerpt: string;
  content: string;
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
