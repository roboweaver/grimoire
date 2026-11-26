import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderWithSpectrum } from "../test-utils";

vi.mock("../api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/client")>();
  return {
    ...actual,
    api: {
      ...actual.api,
      session: vi.fn(),
      posts: vi.fn(),
      authors: vi.fn(),
      deletePost: vi.fn(),
      restorePost: vi.fn(),
      emptyTrash: vi.fn(),
    },
  };
});

import { api } from "../api/client";
import { PostsList } from "./PostsList";

const mocked = {
  session: api.session as unknown as ReturnType<typeof vi.fn>,
  posts: api.posts as unknown as ReturnType<typeof vi.fn>,
  authors: api.authors as unknown as ReturnType<typeof vi.fn>,
  deletePost: api.deletePost as unknown as ReturnType<typeof vi.fn>,
  restorePost: api.restorePost as unknown as ReturnType<typeof vi.fn>,
  emptyTrash: api.emptyTrash as unknown as ReturnType<typeof vi.fn>,
};

function sessionInfo(trashEnabled: boolean) {
  return {
    id: 1,
    login: "ed",
    displayName: "Ed",
    roles: ["administrator"],
    capabilities: [],
    csrfToken: "t",
    trashEnabled,
  };
}

function listOf(status: string, count = 1) {
  const items = Array.from({ length: count }, (_, i) => ({
    id: 10 + i,
    title: `Row ${10 + i}`,
    slug: `row-${10 + i}`,
    type: "post",
    status,
    author: 1,
    date: "2024-01-01T00:00:00Z",
  }));
  return { items, page: 1, perPage: 10, total: count, totalPages: 1 };
}

function renderList(path: string, trashEnabled = true) {
  mocked.session.mockResolvedValue(sessionInfo(trashEnabled));
  mocked.authors.mockResolvedValue({ authors: [] });
  return renderWithSpectrum(
    <MemoryRouter initialEntries={[path]}>
      <PostsList />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  mocked.deletePost.mockResolvedValue(undefined);
  mocked.restorePost.mockResolvedValue(undefined);
});
afterEach(() => vi.clearAllMocks());

describe("PostsList Trash view (Req 18)", () => {
  it("shows Restore and Delete permanently and no Edit per trashed row (Req 18.2)", async () => {
    mocked.posts.mockResolvedValue(listOf("trash"));
    renderList("/posts?status=trash");
    expect(
      await screen.findByRole("button", { name: /restore/i }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /delete permanently/i }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^edit/i })).toBeNull();
  });

  it("Restore calls api.restorePost(id) with no status and reloads (Req 18.5)", async () => {
    mocked.posts.mockResolvedValue(listOf("trash"));
    const user = userEvent.setup();
    renderList("/posts?status=trash");
    await user.click(await screen.findByRole("button", { name: /restore/i }));
    await waitFor(() => expect(mocked.restorePost).toHaveBeenCalledTimes(1));
    expect(mocked.restorePost.mock.calls[0]).toEqual([10]);
    await waitFor(() => expect(mocked.posts.mock.calls.length).toBeGreaterThan(1));
  });

  it("Delete permanently asks for confirmation then calls deletePost(id, {force: true}) (Req 18.4)", async () => {
    mocked.posts.mockResolvedValue(listOf("trash"));
    const user = userEvent.setup();
    renderList("/posts?status=trash");
    await user.click(
      await screen.findByRole("button", { name: /delete permanently/i }),
    );
    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toHaveTextContent(/cannot be undone/i);
    expect(mocked.deletePost).not.toHaveBeenCalled();
    await user.click(
      within(dialog).getByRole("button", { name: /delete permanently|delete/i }),
    );
    await waitFor(() =>
      expect(mocked.deletePost).toHaveBeenCalledWith(10, { force: true }),
    );
  });

  it("Empty Trash confirms, then repeats emptyTrash(type) while more and deleted > 0 (Req 18.2, 18.4)", async () => {
    mocked.posts.mockResolvedValue(listOf("trash"));
    mocked.emptyTrash
      .mockResolvedValueOnce({ deleted: 5, skipped: 0, failed: 0, more: true })
      .mockResolvedValueOnce({ deleted: 2, skipped: 0, failed: 0, more: false });
    const user = userEvent.setup();
    renderList("/posts?status=trash");
    await user.click(await screen.findByRole("button", { name: /empty trash/i }));
    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toHaveTextContent(/cannot be undone/i);
    expect(mocked.emptyTrash).not.toHaveBeenCalled();
    await user.click(
      within(dialog).getByRole("button", { name: /empty trash|delete/i }),
    );
    await waitFor(() => expect(mocked.emptyTrash).toHaveBeenCalledTimes(2));
    expect(mocked.emptyTrash).toHaveBeenNthCalledWith(1, "post");
    expect(mocked.emptyTrash).toHaveBeenNthCalledWith(2, "post");
  });

  it("Empty Trash stops when a response has more but deleted is 0", async () => {
    mocked.posts.mockResolvedValue(listOf("trash"));
    mocked.emptyTrash.mockResolvedValue({
      deleted: 0,
      skipped: 3,
      failed: 0,
      more: true,
    });
    const user = userEvent.setup();
    renderList("/posts?status=trash");
    await user.click(await screen.findByRole("button", { name: /empty trash/i }));
    const dialog = await screen.findByRole("alertdialog");
    await user.click(
      within(dialog).getByRole("button", { name: /empty trash|delete/i }),
    );
    await waitFor(() => expect(mocked.emptyTrash).toHaveBeenCalled());
    await new Promise((r) => setTimeout(r, 50));
    expect(mocked.emptyTrash).toHaveBeenCalledTimes(1);
  });

  it("shows 'Trash is empty' and no Empty Trash button for an empty Trash view", async () => {
    mocked.posts.mockResolvedValue({
      items: [],
      page: 1,
      perPage: 10,
      total: 0,
      totalPages: 0,
    });
    renderList("/posts?status=trash");
    expect(await screen.findByText(/trash is empty/i)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /empty trash/i })).toBeNull();
  });
});

describe("PostsList row action outside Trash (Req 18.3)", () => {
  it("with trashEnabled: Move to Trash, no confirmation, deletePost(id), Undo restores 'previous'", async () => {
    mocked.posts.mockResolvedValue(listOf("publish"));
    const user = userEvent.setup();
    renderList("/posts", true);
    await user.click(await screen.findByRole("button", { name: /move to trash/i }));
    expect(screen.queryByRole("alertdialog")).toBeNull();
    await waitFor(() => expect(mocked.deletePost).toHaveBeenCalledTimes(1));
    expect(mocked.deletePost.mock.calls[0]).toEqual([10]);
    await user.click(await screen.findByRole("button", { name: /undo/i }));
    await waitFor(() =>
      expect(mocked.restorePost).toHaveBeenCalledWith(10, "previous"),
    );
  });

  it("without trashEnabled: Delete permanently behind the destructive dialog, no Move to Trash", async () => {
    mocked.posts.mockResolvedValue(listOf("publish"));
    const user = userEvent.setup();
    renderList("/posts", false);
    const action = await screen.findByRole("button", {
      name: /delete permanently/i,
    });
    expect(screen.queryByRole("button", { name: /move to trash/i })).toBeNull();
    await user.click(action);
    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toHaveTextContent(/cannot be undone/i);
    expect(mocked.deletePost).not.toHaveBeenCalled();
    await user.click(
      within(dialog).getByRole("button", { name: /delete permanently|delete/i }),
    );
    await waitFor(() => expect(mocked.deletePost).toHaveBeenCalledTimes(1));
    expect(mocked.deletePost.mock.calls[0][0]).toBe(10);
  });
});
