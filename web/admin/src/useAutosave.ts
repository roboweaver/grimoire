import { useEffect, useState } from "react";
import { api, ApiError } from "./api/client";
import type { AutosaveDetail, AutosaveWriteInput } from "./api/types";

interface UseAutosaveOptions {
  // postId is undefined for a not-yet-created post; autosave is disabled
  // entirely in that case (there is nothing to autosave against yet).
  postId: number | string | undefined;
  // postModified is the currently-loaded post's own last-modified timestamp,
  // used to decide whether a found autosave is actually newer (Req 8.5).
  postModified: string | undefined;
  // isDirty reflects whether the editor currently has unsaved changes;
  // the periodic autosave only runs while this is true (Req 8.4).
  isDirty: boolean;
  // getSnapshot returns the current title/content/excerpt to autosave. It is
  // read fresh on every tick rather than captured once, so edits made after
  // the hook mounts are still picked up.
  getSnapshot: () => AutosaveWriteInput;
  intervalMs?: number;
}

interface UseAutosaveResult {
  // notice is the newer-than-the-post autosave found on mount, or null if
  // none exists (or none is newer, or it was dismissed). It is purely
  // informational (Req 8.5, 8.6) — the caller decides whether/how to offer
  // loading it into the editor.
  notice: AutosaveDetail | null;
  dismissNotice: () => void;
}

const DEFAULT_INTERVAL_MS = 15000;

// useAutosave periodically autosaves unsaved editor changes (Req 8.4) and,
// on mount, surfaces a dismissible notice if a newer autosave already exists
// for this post (Req 8.5) without ever auto-applying it or blocking a normal
// manual save (Req 8.6). Autosave never calls the normal save path or
// creates a normal revision snapshot — it only ever talks to the dedicated
// autosave endpoints.
export function useAutosave({
  postId,
  postModified,
  isDirty,
  getSnapshot,
  intervalMs = DEFAULT_INTERVAL_MS,
}: UseAutosaveOptions): UseAutosaveResult {
  const [notice, setNotice] = useState<AutosaveDetail | null>(null);

  useEffect(() => {
    if (postId == null) return;
    let cancelled = false;
    api
      .getAutosave(postId)
      .then((autosave) => {
        if (cancelled) return;
        const isNewer =
          !postModified || new Date(autosave.modified).getTime() > new Date(postModified).getTime();
        if (isNewer) setNotice(autosave);
      })
      .catch((err: unknown) => {
        // A 404 (no autosave / none newer / unauthorized / post has none)
        // is the normal, expected outcome, not an error to surface.
        if (err instanceof ApiError && err.status === 404) return;
        // Any other failure is likewise non-blocking (Req 8.6): autosave
        // status is informational only and never disrupts editing.
      });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [postId]);

  useEffect(() => {
    if (postId == null || !isDirty) return;
    const timer = setInterval(() => {
      void api.saveAutosave(postId, getSnapshot());
    }, intervalMs);
    return () => clearInterval(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [postId, isDirty, intervalMs]);

  return {
    notice,
    dismissNotice: () => setNotice(null),
  };
}
