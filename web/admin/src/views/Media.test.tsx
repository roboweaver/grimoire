import { fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import { renderWithSpectrum } from "../test-utils";

vi.mock("../api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/client")>();
  return {
    ...actual,
    api: { ...actual.api, media: vi.fn(), posts: vi.fn() },
  };
});

import { api } from "../api/client";
import { Media } from "./Media";

const ITEM = {
  id: 201,
  title: "Photo",
  filename: "photo.jpg",
  url: "/u/photo.jpg",
  mimeType: "image/jpeg",
  date: "2024-01-06T00:00:00Z",
  parentId: 1,
};
const PARENT_POST = {
  id: 1,
  title: "Hello world",
  slug: "hello-world",
  type: "post",
  status: "publish",
  author: 1,
  date: "2024-01-01T00:00:00Z",
};

function mockDefaults() {
  vi.mocked(api.media).mockResolvedValue({
    items: [ITEM],
    page: 1,
    perPage: 20,
    total: 1,
    totalPages: 1,
  });
  vi.mocked(api.posts).mockResolvedValue({
    items: [PARENT_POST],
    page: 1,
    perPage: 100,
    total: 1,
    totalPages: 1,
  });
}

describe("Media filters and view toggle", () => {
  it("sends search and type query params typed into the filter bar", async () => {
    mockDefaults();
    const user = userEvent.setup();
    renderWithSpectrum(
      <MemoryRouter initialEntries={["/media"]}>
        <Media />
      </MemoryRouter>,
    );
    await waitFor(() => expect(api.media).toHaveBeenCalled());

    await user.type(screen.getByLabelText(/search/i), "jpg");
    await waitFor(() =>
      expect(api.media).toHaveBeenLastCalledWith(
        expect.objectContaining({ search: "jpg" }),
        expect.anything(),
      ),
    );

    await user.click(screen.getByRole("button", { name: /type/i }));
    await user.click(await screen.findByRole("option", { name: /^image$/i }));
    await waitFor(() =>
      expect(api.media).toHaveBeenLastCalledWith(
        expect.objectContaining({ type: "image" }),
        expect.anything(),
      ),
    );
  });

  it("sends after/before/parentId query params from the date-range and parent-post filters (Req 5.6)", async () => {
    mockDefaults();
    const user = userEvent.setup();
    renderWithSpectrum(
      <MemoryRouter initialEntries={["/media"]}>
        <Media />
      </MemoryRouter>,
    );
    await waitFor(() => expect(api.posts).toHaveBeenCalled());

    fireEvent.change(screen.getByLabelText(/from date/i), {
      target: { value: "2024-01-01" },
    });
    await waitFor(() =>
      expect(api.media).toHaveBeenLastCalledWith(
        expect.objectContaining({ after: "2024-01-01" }),
        expect.anything(),
      ),
    );

    fireEvent.change(screen.getByLabelText(/to date/i), {
      target: { value: "2024-01-31" },
    });
    await waitFor(() =>
      expect(api.media).toHaveBeenLastCalledWith(
        expect.objectContaining({ before: "2024-01-31" }),
        expect.anything(),
      ),
    );

    await user.click(screen.getByRole("button", { name: /parent post/i }));
    await user.click(await screen.findByRole("option", { name: /hello world/i }));
    await waitFor(() =>
      expect(api.media).toHaveBeenLastCalledWith(
        expect.objectContaining({ parentId: 1 }),
        expect.anything(),
      ),
    );
  });

  it("shows exactly one of grid or list view, toggled via the keyboard, never both (Req 6, Req 9.3)", async () => {
    mockDefaults();
    const user = userEvent.setup();
    renderWithSpectrum(
      <MemoryRouter initialEntries={["/media"]}>
        <Media />
      </MemoryRouter>,
    );
    await waitFor(() => expect(screen.getByText("Photo")).toBeInTheDocument());

    expect(screen.getByTestId("media-grid-view")).toBeInTheDocument();
    expect(screen.queryByTestId("media-list-view")).not.toBeInTheDocument();

    const listToggle = screen.getByRole("button", { name: /list view/i });
    listToggle.focus();
    await user.keyboard("{Enter}");
    expect(await screen.findByTestId("media-list-view")).toBeInTheDocument();
    expect(screen.queryByTestId("media-grid-view")).not.toBeInTheDocument();
  });

  it("persists the list view across a reload via the URL (Req 6.4)", async () => {
    mockDefaults();
    renderWithSpectrum(
      <MemoryRouter initialEntries={["/media?view=list"]}>
        <Media />
      </MemoryRouter>,
    );
    expect(await screen.findByTestId("media-list-view")).toBeInTheDocument();
    expect(screen.queryByTestId("media-grid-view")).not.toBeInTheDocument();
  });
});
