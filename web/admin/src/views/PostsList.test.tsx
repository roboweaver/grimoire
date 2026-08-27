import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import { renderWithSpectrum } from "../test-utils";

vi.mock("../api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/client")>();
  return {
    ...actual,
    api: { ...actual.api, posts: vi.fn(), authors: vi.fn() },
  };
});

import { api } from "../api/client";
import { PostsList } from "./PostsList";

describe("PostsList filters", () => {
  it("sends the search and status query params typed into the filter bar", async () => {
    vi.mocked(api.posts).mockResolvedValue({
      items: [],
      page: 1,
      perPage: 10,
      total: 0,
      totalPages: 0,
    });
    vi.mocked(api.authors).mockResolvedValue({
      authors: [{ id: 1, displayName: "Ed Author" }],
    });
    const user = userEvent.setup();
    renderWithSpectrum(
      <MemoryRouter initialEntries={["/posts"]}>
        <PostsList />
      </MemoryRouter>,
    );
    await waitFor(() => expect(api.posts).toHaveBeenCalled());

    await user.type(screen.getByLabelText(/search/i), "hello");
    await waitFor(() =>
      expect(api.posts).toHaveBeenLastCalledWith(
        expect.objectContaining({ search: "hello" }),
        expect.anything(),
      ),
    );

    await user.click(screen.getByRole("button", { name: /status/i }));
    await user.click(await screen.findByRole("option", { name: /draft/i }));
    await waitFor(() =>
      expect(api.posts).toHaveBeenLastCalledWith(
        expect.objectContaining({ status: "draft" }),
        expect.anything(),
      ),
    );
  });

  it("loads with no filters applied and does not error (Req 4.5)", async () => {
    vi.mocked(api.posts).mockResolvedValue({
      items: [],
      page: 1,
      perPage: 10,
      total: 0,
      totalPages: 0,
    });
    vi.mocked(api.authors).mockResolvedValue({ authors: [] });
    renderWithSpectrum(
      <MemoryRouter initialEntries={["/posts"]}>
        <PostsList />
      </MemoryRouter>,
    );
    await waitFor(() =>
      expect(api.posts).toHaveBeenCalledWith(
        expect.not.objectContaining({ search: expect.anything() }),
        expect.anything(),
      ),
    );
  });
});
