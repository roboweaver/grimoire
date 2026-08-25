import { renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useAutosave } from "./useAutosave";

vi.mock("./api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./api/client")>();
  return {
    ...actual,
    api: {
      ...actual.api,
      getAutosave: vi.fn(),
      saveAutosave: vi.fn(),
    },
  };
});

import { ApiError } from "./api/client";
import { api } from "./api/client";

const getAutosave = api.getAutosave as unknown as ReturnType<typeof vi.fn>;
const saveAutosave = api.saveAutosave as unknown as ReturnType<typeof vi.fn>;

afterEach(() => {
  vi.clearAllMocks();
  vi.useRealTimers();
});

describe("useAutosave", () => {
  it("periodically calls saveAutosave while the post has unsaved changes (Req 8.4)", async () => {
    vi.useFakeTimers();
    getAutosave.mockRejectedValue(new ApiError(404, "not_found", "none"));
    const snapshot = { title: "t", content: "c", excerpt: "e" };

    renderHook(() =>
      useAutosave({ postId: 1, postModified: undefined, isDirty: true, getSnapshot: () => snapshot, intervalMs: 5000 }),
    );

    await vi.advanceTimersByTimeAsync(5000);
    expect(saveAutosave).toHaveBeenCalledWith(1, snapshot);

    await vi.advanceTimersByTimeAsync(5000);
    expect(saveAutosave).toHaveBeenCalledTimes(2);
  });

  it("does not call saveAutosave (or any normal save) when there are no unsaved changes (Req 8.4)", async () => {
    vi.useFakeTimers();
    getAutosave.mockRejectedValue(new ApiError(404, "not_found", "none"));
    const snapshot = { title: "t", content: "c", excerpt: "e" };

    renderHook(() =>
      useAutosave({ postId: 1, postModified: undefined, isDirty: false, getSnapshot: () => snapshot, intervalMs: 5000 }),
    );

    await vi.advanceTimersByTimeAsync(15000);
    expect(saveAutosave).not.toHaveBeenCalled();
  });

  it("surfaces a dismissible notice when a newer autosave exists on mount, without applying it (Req 8.5)", async () => {
    getAutosave.mockResolvedValue({
      title: "autosaved title",
      content: "autosaved content",
      excerpt: "",
      modified: "2024-06-02T00:00:00Z",
    });

    const { result } = renderHook(() =>
      useAutosave({
        postId: 1,
        postModified: "2024-06-01T00:00:00Z",
        isDirty: false,
        getSnapshot: () => ({ title: "t", content: "c", excerpt: "e" }),
      }),
    );

    await waitFor(() => expect(result.current.notice).not.toBeNull());
    expect(result.current.notice?.title).toBe("autosaved title");

    result.current.dismissNotice();
    await waitFor(() => expect(result.current.notice).toBeNull());
  });

  it("does not surface a notice when the autosave is not newer than the post (Req 8.5)", async () => {
    getAutosave.mockResolvedValue({
      title: "autosaved title",
      content: "autosaved content",
      excerpt: "",
      modified: "2024-01-01T00:00:00Z",
    });

    const { result } = renderHook(() =>
      useAutosave({
        postId: 1,
        postModified: "2024-06-01T00:00:00Z",
        isDirty: false,
        getSnapshot: () => ({ title: "t", content: "c", excerpt: "e" }),
      }),
    );

    await waitFor(() => expect(getAutosave).toHaveBeenCalled());
    expect(result.current.notice).toBeNull();
  });

  it("treats a 404 from getAutosave as no autosave, not an error (Req 8.5)", async () => {
    getAutosave.mockRejectedValue(new ApiError(404, "not_found", "no autosave"));

    const { result } = renderHook(() =>
      useAutosave({ postId: 1, postModified: undefined, isDirty: false, getSnapshot: () => ({ title: "", content: "", excerpt: "" }) }),
    );

    await waitFor(() => expect(getAutosave).toHaveBeenCalled());
    expect(result.current.notice).toBeNull();
  });
});
