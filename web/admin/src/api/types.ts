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
