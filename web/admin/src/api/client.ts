import type {
  ApiErrorBody,
  CommentList,
  MediaItem,
  MediaList,
  NavMenu,
  PostDetail,
  PostList,
  SessionInfo,
  Stats,
} from "./types";

// ForbiddenError signals an authenticated-but-uncapable response (403). Views
// surface it as an "insufficient permissions" state (Req 8.4).
export class ForbiddenError extends Error {
  constructor(message = "insufficient permissions") {
    super(message);
    this.name = "ForbiddenError";
  }
}

// ApiError carries the server error envelope for display in error states.
export class ApiError extends Error {
  code: string;
  status: number;
  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
  }
}

// redirectToLogin sends the browser to the login page, preserving the current
// admin path so the user returns here after authenticating (Req 8.4).
function redirectToLogin(): void {
  const here = window.location.pathname + window.location.search;
  window.location.assign(`/login?redirect=${encodeURIComponent(here)}`);
}

async function get<T>(path: string, signal?: AbortSignal): Promise<T> {
  let res: Response;
  try {
    res = await fetch(`/admin/api${path}`, {
      method: "GET",
      credentials: "include",
      headers: { Accept: "application/json" },
      signal,
    });
  } catch (err) {
    if ((err as Error).name === "AbortError") throw err;
    throw new ApiError(0, "network_error", "Unable to reach the server.");
  }

  if (res.status === 401) {
    // Session expired or absent — bounce to login rather than render an error.
    redirectToLogin();
    throw new ApiError(401, "unauthorized", "authentication required");
  }
  if (res.status === 403) {
    throw new ForbiddenError();
  }

  if (!res.ok) {
    let code = "error";
    let message = `Request failed (${res.status}).`;
    try {
      const body = (await res.json()) as ApiErrorBody;
      if (body?.error) {
        code = body.error.code || code;
        message = body.error.message || message;
      }
    } catch {
      // Non-JSON error body; keep the generic message.
    }
    throw new ApiError(res.status, code, message);
  }

  return (await res.json()) as T;
}


async function send<T>(path: string, init: RequestInit = {}): Promise<T> {
  let res: Response;
  const session = await get<SessionInfo>("/session");
  try {
    res = await fetch(`/admin/api${path}`, {
      ...init,
      credentials: "include",
      headers: {
        Accept: "application/json",
        "X-CSRF-Token": session.csrfToken,
        ...(init.headers || {}),
      },
    });
  } catch (err) {
    if ((err as Error).name === "AbortError") throw err;
    throw new ApiError(0, "network_error", "Unable to reach the server.");
  }

  if (res.status === 401) {
    redirectToLogin();
    throw new ApiError(401, "unauthorized", "authentication required");
  }
  if (res.status === 403) {
    throw new ForbiddenError();
  }
  if (!res.ok) {
    let code = "error";
    let message = `Request failed (${res.status}).`;
    try {
      const body = (await res.json()) as ApiErrorBody;
      if (body?.error) {
        code = body.error.code || code;
        message = body.error.message || message;
      }
    } catch {}
    throw new ApiError(res.status, code, message);
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}
export const api = {
  session: (signal?: AbortSignal) => get<SessionInfo>("/session", signal),
  stats: (signal?: AbortSignal) => get<Stats>("/stats", signal),
  posts: (
    params: { page?: number; perPage?: number; type?: string; status?: string },
    signal?: AbortSignal,
  ) => {
    const q = new URLSearchParams();
    if (params.page) q.set("page", String(params.page));
    if (params.perPage) q.set("perPage", String(params.perPage));
    if (params.type) q.set("type", params.type);
    if (params.status) q.set("status", params.status);
    const qs = q.toString();
    return get<PostList>(`/posts${qs ? `?${qs}` : ""}`, signal);
  },
  post: (id: number | string, signal?: AbortSignal) =>
    get<PostDetail>(`/posts/${id}`, signal),
  comments: (params: { page?: number; perPage?: number; status?: string; postId?: number }, signal?: AbortSignal) => {
    const q = new URLSearchParams();
    if (params.page) q.set("page", String(params.page));
    if (params.perPage) q.set("perPage", String(params.perPage));
    if (params.status) q.set("status", params.status);
    if (params.postId) q.set("postId", String(params.postId));
    const qs = q.toString();
    return get<CommentList>(`/comments${qs ? `?${qs}` : ""}`, signal);
  },
  moderateComment: (id: number, status: string) =>
    send<{ id: number; status: string }>(`/comments/${id}/status`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ status }),
    }),
  media: (params: { page?: number; perPage?: number }, signal?: AbortSignal) => {
    const q = new URLSearchParams();
    if (params.page) q.set("page", String(params.page));
    if (params.perPage) q.set("perPage", String(params.perPage));
    const qs = q.toString();
    return get<MediaList>(`/media${qs ? `?${qs}` : ""}`, signal);
  },
  uploadMedia: (file: File) => {
    const form = new FormData();
    form.append("file", file);
    return send<MediaItem>("/media", { method: "POST", body: form });
  },
  attachMedia: (id: number, parentId: number) =>
    send<{ id: number; parentId: number }>(`/media/${id}/attach`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ parentId }),
    }),
  menus: (signal?: AbortSignal) => get<{ items: NavMenu[] }>("/menus", signal),
};
