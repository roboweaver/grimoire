import { Provider, defaultTheme } from "@adobe/react-spectrum";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { RevisionsPanel } from "./RevisionsPanel";

vi.mock("../api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/client")>();
  return {
    ...actual,
    api: {
      ...actual.api,
      listRevisions: vi.fn(),
      getRevision: vi.fn(),
      restoreRevision: vi.fn(),
    },
  };
});

import { api } from "../api/client";

const listRevisions = api.listRevisions as unknown as ReturnType<typeof vi.fn>;
const getRevision = api.getRevision as unknown as ReturnType<typeof vi.fn>;
const restoreRevision = api.restoreRevision as unknown as ReturnType<typeof vi.fn>;

function renderPanel(props: { currentContent: string; onRestored: () => void }) {
  return render(
    <Provider theme={defaultTheme}>
      <RevisionsPanel postId={1} {...props} />
    </Provider>,
  );
}

describe("RevisionsPanel", () => {
  it("fetches and lists revisions on mount (Req 8.1)", async () => {
    listRevisions.mockResolvedValue([
      { id: 5, author: 2, modified: "2024-01-02T03:04:00Z" },
      { id: 4, author: 3, modified: "2024-01-01T03:04:00Z" },
    ]);
    renderPanel({ currentContent: "current", onRestored: vi.fn() });

    expect(listRevisions).toHaveBeenCalledWith(1, expect.anything());
    expect(await screen.findByRole("button", { name: /author 2/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /author 3/i })).toBeInTheDocument();
  });

  it("selecting a revision shows a diff against the current content (Req 8.2)", async () => {
    listRevisions.mockResolvedValue([{ id: 5, author: 2, modified: "2024-01-02T03:04:00Z" }]);
    getRevision.mockResolvedValue({ id: 5, title: "T", content: "the old words", excerpt: "", modified: "2024-01-02T03:04:00Z" });
    renderPanel({ currentContent: "the new words", onRestored: vi.fn() });

    const entry = await screen.findByRole("button", { name: /author 2/i });
    await userEvent.click(entry);

    await waitFor(() => expect(getRevision).toHaveBeenCalledWith(1, 5));
    const diff = await screen.findByTestId("revision-diff");
    expect(within(diff).getAllByTestId("diff-removed").length).toBeGreaterThan(0);
    expect(within(diff).getAllByTestId("diff-added").length).toBeGreaterThan(0);
    expect(within(diff).getByText("old")).toBeInTheDocument();
    expect(within(diff).getByText("new")).toBeInTheDocument();
  });

  it("falls back to a whole-text replacement for large comparisons", async () => {
    const oldContent = Array.from({ length: 225 }, (_, i) => `old-${i}`).join(" ");
    const currentContent = Array.from({ length: 225 }, (_, i) => `new-${i}`).join(" ");
    listRevisions.mockResolvedValue([{ id: 5, author: 2, modified: "2024-01-02T03:04:00Z" }]);
    getRevision.mockResolvedValue({
      id: 5,
      title: "T",
      content: oldContent,
      excerpt: "",
      modified: "2024-01-02T03:04:00Z",
    });
    renderPanel({ currentContent, onRestored: vi.fn() });

    await userEvent.click(await screen.findByRole("button", { name: /author 2/i }));

    const diff = await screen.findByTestId("revision-diff");
    expect(within(diff).getAllByTestId("diff-removed")).toHaveLength(1);
    expect(within(diff).getAllByTestId("diff-added")).toHaveLength(1);
    expect(within(diff).getByTestId("diff-removed")).toHaveTextContent(oldContent);
    expect(within(diff).getByTestId("diff-added")).toHaveTextContent(currentContent);
  });

  it('offers "Restore this revision", which calls the restore endpoint and reloads the editor (Req 8.3)', async () => {
    listRevisions.mockResolvedValue([{ id: 5, author: 2, modified: "2024-01-02T03:04:00Z" }]);
    getRevision.mockResolvedValue({ id: 5, title: "T", content: "old", excerpt: "", modified: "2024-01-02T03:04:00Z" });
    restoreRevision.mockResolvedValue({ id: 1, title: "T", content: "old" });
    const onRestored = vi.fn();
    renderPanel({ currentContent: "new", onRestored });

    await userEvent.click(await screen.findByRole("button", { name: /author 2/i }));
    await waitFor(() => expect(getRevision).toHaveBeenCalled());

    await userEvent.click(await screen.findByRole("button", { name: /restore this revision/i }));

    await waitFor(() => expect(restoreRevision).toHaveBeenCalledWith(1, 5));
    expect(onRestored).toHaveBeenCalled();
  });
});
