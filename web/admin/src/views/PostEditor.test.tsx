import { Provider, defaultTheme } from "@adobe/react-spectrum";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { PostDetail, TermSummary } from "../api/types";
import { PostEditor } from "./PostEditor";

vi.mock("../api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/client")>();
  return {
    ...actual,
    api: { ...actual.api, post: vi.fn(), createPost: vi.fn(), updatePost: vi.fn(), deletePost: vi.fn() },
  };
});

// RichTextEditor, TermPicker and RevisionsPanel have their own dedicated
// test suites (RichTextEditor.test.tsx, TermPicker.test.tsx,
// RevisionsPanel.test.tsx); PostEditor's tests stub them with minimal
// controllable doubles so failures here are about PostEditor's own
// load/save/conflict wiring, not TipTap/Spectrum internals or revision
// fetches.
vi.mock("../components/RichTextEditor", () => ({
  RichTextEditor: ({ content, onChange }: { content: string; onChange: (html: string) => void }) => (
    <textarea
      aria-label="Content"
      data-testid="richtext-stub"
      value={content}
      onChange={(e) => onChange(e.target.value)}
    />
  ),
}));
vi.mock("../components/RevisionsPanel", () => ({
  RevisionsPanel: () => <div data-testid="revisions-panel-stub" />,
}));
vi.mock("../components/TermPicker", () => ({
  TermPicker: ({ label }: { label: string; selected: TermSummary[]; onChange: (next: TermSummary[]) => void }) => (
    <div data-testid={`term-picker-${label}`}>{label}</div>
  ),
}));

import { api } from "../api/client";

const apiPost = api.post as unknown as ReturnType<typeof vi.fn>;
const createPost = api.createPost as unknown as ReturnType<typeof vi.fn>;
const updatePost = api.updatePost as unknown as ReturnType<typeof vi.fn>;
const deletePost = api.deletePost as unknown as ReturnType<typeof vi.fn>;

afterEach(() => vi.clearAllMocks());

function detail(overrides: Partial<PostDetail> = {}): PostDetail {
  return {
    id: 7,
    title: "Existing title",
    slug: "existing-title",
    type: "post",
    status: "draft",
    author: 1,
    date: "2024-01-01T00:00:00Z",
    modified: "2024-01-01T00:00:00Z",
    excerpt: "An excerpt",
    content: "<p>Hello</p>",
    commentStatus: "open",
    terms: { category: [], post_tag: [] },
    ...overrides,
  };
}

function renderEditor(path: string) {
  return render(
    <Provider theme={defaultTheme}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/posts/new" element={<PostEditor type="post" />} />
          <Route path="/posts/:id" element={<PostEditor type="post" />} />
        </Routes>
      </MemoryRouter>
    </Provider>,
  );
}

describe("PostEditor", () => {
  it("loads an existing post's fields into the title/content/status/term pickers", async () => {
    apiPost.mockResolvedValue(detail({ title: "Existing title", status: "draft" }));
    renderEditor("/posts/7");

    expect(await screen.findByDisplayValue("Existing title")).toBeInTheDocument();
    expect(screen.getByTestId("richtext-stub")).toHaveValue("<p>Hello</p>");
    expect(screen.getByTestId("term-picker-Categories")).toBeInTheDocument();
    expect(screen.getByTestId("term-picker-Tags")).toBeInTheDocument();
    expect(apiPost).toHaveBeenCalledWith("7", expect.anything());
  });

  it("calls createPost (no modified field) when there is no existing id", async () => {
    createPost.mockResolvedValue(detail({ id: 99, title: "Brand new" }));
    renderEditor("/posts/new");

    await userEvent.type(await screen.findByLabelText(/title/i), "Brand new");
    await userEvent.click(screen.getByRole("button", { name: /^save$/i }));

    await waitFor(() => expect(createPost).toHaveBeenCalled());
    const body = createPost.mock.calls[0][0];
    expect(body.title).toBe("Brand new");
    expect(body.modified).toBeUndefined();
  });

  it("calls updatePost with the loaded modified value when editing an existing post", async () => {
    apiPost.mockResolvedValue(detail({ id: 7, modified: "2024-01-01T00:00:00Z" }));
    updatePost.mockResolvedValue(detail({ id: 7, modified: "2024-01-02T00:00:00Z" }));
    renderEditor("/posts/7");

    await screen.findByDisplayValue("Existing title");
    await userEvent.click(screen.getByRole("button", { name: /^save$/i }));

    await waitFor(() => expect(updatePost).toHaveBeenCalled());
    const [id, body] = updatePost.mock.calls[0];
    expect(id).toBe("7");
    expect(body.modified).toBe("2024-01-01T00:00:00Z");
  });

  it("opens ConflictDialog when save returns a conflict, without discarding local edits", async () => {
    apiPost.mockResolvedValue(detail({ id: 7, modified: "2024-01-01T00:00:00Z" }));
    const { ConflictError } = await import("../api/client");
    updatePost.mockRejectedValue(new ConflictError("2024-01-02T00:00:00Z"));
    renderEditor("/posts/7");

    await screen.findByDisplayValue("Existing title");
    await userEvent.click(screen.getByRole("button", { name: /^save$/i }));

    expect(await screen.findByText(/changed since you loaded it/i)).toBeInTheDocument();
    expect(screen.getByText(/2024-01-02T00:00:00Z/)).toBeInTheDocument();
    // Local edits (the title field) must still be present, not reset.
    expect(screen.getByDisplayValue("Existing title")).toBeInTheDocument();
  });

  it("shows a generic error banner (without navigating away) on a 403", async () => {
    apiPost.mockResolvedValue(detail({ id: 7 }));
    const { ForbiddenError } = await import("../api/client");
    updatePost.mockRejectedValue(new ForbiddenError());
    renderEditor("/posts/7");

    await screen.findByDisplayValue("Existing title");
    await userEvent.click(screen.getByRole("button", { name: /^save$/i }));

    expect(await screen.findByText(/insufficient permissions|permission/i)).toBeInTheDocument();
    // Still on the editor — the title field is still rendered.
    expect(screen.getByDisplayValue("Existing title")).toBeInTheDocument();
    expect(deletePost).not.toHaveBeenCalled();
  });

  it("omits date from the save body for every non-future status", async () => {
    apiPost.mockResolvedValue(detail({ id: 7, status: "draft" }));
    updatePost.mockResolvedValue(detail({ id: 7, status: "draft" }));
    renderEditor("/posts/7");

    await screen.findByDisplayValue("Existing title");
    await userEvent.click(screen.getByRole("button", { name: /^save$/i }));

    await waitFor(() => expect(updatePost).toHaveBeenCalled());
    const [, body] = updatePost.mock.calls[0];
    expect(body.date).toBeUndefined();
  });

  it("shows a schedule date picker only when status is future, preloaded from the loaded post's date", async () => {
    apiPost.mockResolvedValue(detail({ id: 7, status: "future", date: "2099-06-01T12:00:00Z" }));
    renderEditor("/posts/7");

    await screen.findByDisplayValue("Existing title");
    expect(screen.getAllByLabelText(/publish date/i).length).toBeGreaterThan(0);
  });

  it("does not render the schedule date picker for a draft post", async () => {
    apiPost.mockResolvedValue(detail({ id: 7, status: "draft" }));
    renderEditor("/posts/7");

    await screen.findByDisplayValue("Existing title");
    expect(screen.queryAllByLabelText(/publish date/i).length).toBe(0);
  });

  it("includes date in the save body when status is future and a schedule date is set", async () => {
    apiPost.mockResolvedValue(detail({ id: 7, status: "future", date: "2099-06-01T12:00:00Z" }));
    updatePost.mockResolvedValue(detail({ id: 7, status: "future", date: "2099-06-01T12:00:00Z" }));
    renderEditor("/posts/7");

    await screen.findByDisplayValue("Existing title");
    await userEvent.click(screen.getByRole("button", { name: /^save$/i }));

    await waitFor(() => expect(updatePost).toHaveBeenCalled());
    const [, body] = updatePost.mock.calls[0];
    expect(body.date).toBe("2099-06-01T12:00:00.000Z");
  });

  // Guards the server-side fix where the future-date exemption for
  // "unchanged date" now only applies to a post that was ALREADY
  // future-status. If the SPA preloaded a draft/published post's
  // (necessarily past) stored date into the picker unconditionally, flipping
  // status to "future" without touching the picker would silently resubmit
  // that past date and fail the now-tightened server validation. The picker
  // must instead start empty in that case, forcing an explicit future pick.
  it("does not preload a past stored date into the picker when a non-future post is switched to future", async () => {
    // Midday UTC keeps the fixture's calendar date stable across every
    // timezone parseAbsoluteToLocal might localize into (a midnight-UTC
    // fixture shifts to the prior day west of UTC, which previously let
    // this assertion pass even with the preload bug present, since the
    // buggy year rendered was "2019" rather than the "2020" being matched
    // against).
    apiPost.mockResolvedValue(detail({ id: 7, status: "draft", date: "2020-07-01T12:00:00Z" }));
    renderEditor("/posts/7");

    await screen.findByDisplayValue("Existing title");
    await userEvent.click(screen.getByRole("button", { name: /status/i }));
    await userEvent.click(await screen.findByRole("option", { name: /^future$/i }));

    // Scope to the individual editable date/time spinbutton segments
    // (month/day/year/hour/minute/AM-PM), not the enclosing group (whose
    // text concatenates all segments) or the time-zone segment. The
    // time-zone segment is `role="textbox"`, not `role="spinbutton"`, and
    // legitimately renders digit-containing ICU zone names with no short
    // abbreviation (e.g. "GMT+5:30" for Asia/Kolkata, "GMT+14" for
    // Pacific/Kiritimati) regardless of whether a date is loaded --
    // asserting "no digits anywhere" would false-fail in those zones even
    // on correct code.
    const segments = screen
      .getAllByLabelText(/publish date/i)
      .filter((el) => el.getAttribute("role") === "spinbutton");
    expect(segments.length).toBeGreaterThan(0);
    // Spectrum renders these segments with placeholder text (e.g. "mm",
    // "dd", "yyyy") when the value is null. Assert no digits at all are
    // rendered in any date/time segment -- a stronger, timezone-independent
    // check than matching a specific stale year, since a null value never
    // renders any digit regardless of which calendar date the preloaded
    // fixture would have localized to.
    for (const segment of segments) {
      expect(segment.textContent ?? "").not.toMatch(/\d/);
    }
  });
});
