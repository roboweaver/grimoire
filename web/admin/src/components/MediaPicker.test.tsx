import { Provider, defaultTheme } from "@adobe/react-spectrum";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { MediaItem } from "../api/types";
import { MediaPicker } from "./MediaPicker";

vi.mock("../api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/client")>();
  return {
    ...actual,
    api: { ...actual.api, media: vi.fn() },
  };
});

import { api } from "../api/client";

const mediaApi = api.media as unknown as ReturnType<typeof vi.fn>;

function makeItem(overrides: Partial<MediaItem> = {}): MediaItem {
  return {
    id: 1,
    title: "Sunset",
    filename: "sunset.jpg",
    url: "https://example.test/sunset.jpg",
    mimeType: "image/jpeg",
    date: "2024-01-01T00:00:00Z",
    parentId: 0,
    ...overrides,
  };
}

function renderPicker(props: {
  isOpen: boolean;
  onOpenChange: (open: boolean) => void;
  onSelect: (item: MediaItem) => void;
}) {
  return render(
    <Provider theme={defaultTheme}>
      <MediaPicker {...props} />
    </Provider>,
  );
}

describe("MediaPicker", () => {
  it("lists media items and calls onSelect + closes on pick", async () => {
    mediaApi.mockResolvedValue({
      items: [makeItem({ id: 1, title: "Sunset" }), makeItem({ id: 2, title: "Mountains", filename: "mountains.jpg" })],
      page: 1,
      perPage: 20,
      total: 2,
      totalPages: 1,
    });
    const onSelect = vi.fn();
    const onOpenChange = vi.fn();
    renderPicker({ isOpen: true, onOpenChange, onSelect });

    const button = await screen.findByRole("button", { name: /Sunset/ });
    await userEvent.click(button);

    expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ id: 1, title: "Sunset" }));
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("shows an empty state when there is no media", async () => {
    mediaApi.mockResolvedValue({ items: [], page: 1, perPage: 20, total: 0, totalPages: 0 });
    renderPicker({ isOpen: true, onOpenChange: vi.fn(), onSelect: vi.fn() });

    await waitFor(() => expect(screen.getByText(/No media/i)).toBeInTheDocument());
  });

  it("does not call the media API when closed", () => {
    renderPicker({ isOpen: false, onOpenChange: vi.fn(), onSelect: vi.fn() });
    expect(mediaApi).not.toHaveBeenCalled();
  });
});
