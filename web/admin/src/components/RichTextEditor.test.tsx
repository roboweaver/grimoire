import { Provider, defaultTheme } from "@adobe/react-spectrum";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { act } from "react";
import { describe, expect, it, vi } from "vitest";
import { RichTextEditor } from "./RichTextEditor";

vi.mock("../api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/client")>();
  return {
    ...actual,
    api: {
      ...actual.api,
      media: vi.fn().mockResolvedValue({ items: [], page: 1, perPage: 20, total: 0, totalPages: 0 }),
    },
  };
});

function Harness({ initial, onChange }: { initial: string; onChange: (html: string) => void }) {
  return (
    <Provider theme={defaultTheme}>
      <RichTextEditor content={initial} onChange={onChange} />
    </Provider>
  );
}

describe("RichTextEditor", () => {
  it("renders the initial HTML content", async () => {
    render(<Harness initial="<p>Hello world</p>" onChange={vi.fn()} />);
    await waitFor(() => expect(screen.getByText("Hello world")).toBeInTheDocument());
  });

  it("fires onChange with updated HTML after typing", async () => {
    const onChange = vi.fn();
    render(<Harness initial="<p></p>" onChange={onChange} />);

    const surface = await screen.findByTestId("richtext-surface");
    const editable = surface.querySelector('[contenteditable="true"]') as HTMLElement;
    await userEvent.click(editable);
    await userEvent.type(editable, "Hi");

    await waitFor(() => {
      const calls = onChange.mock.calls;
      const lastHtml = calls[calls.length - 1]?.[0] as string;
      expect(lastHtml).toContain("Hi");
    });
  });

  it("reflects bold active state on the toolbar button and toggles it", async () => {
    const onChange = vi.fn();
    render(<Harness initial="<p>Hello world</p>" onChange={onChange} />);

    const boldButton = await screen.findByRole("button", { name: "Bold" });
    expect(boldButton).toHaveAttribute("aria-pressed", "false");

    const surface = await screen.findByTestId("richtext-surface");
    const editable = surface.querySelector('[contenteditable="true"]') as HTMLElement;
    await userEvent.click(editable);
    // Select the whole paragraph via the browser Selection API so the
    // toolbar's toggleBold() has something to act on.
    await act(async () => {
      const range = document.createRange();
      range.selectNodeContents(editable);
      const selection = window.getSelection();
      selection?.removeAllRanges();
      selection?.addRange(range);
      editable.dispatchEvent(new Event("selectionchange", { bubbles: true }));
    });

    await userEvent.click(boldButton);

    await waitFor(() => expect(boldButton).toHaveAttribute("aria-pressed", "true"));
    await waitFor(() => {
      const calls = onChange.mock.calls;
      const lastHtml = calls[calls.length - 1]?.[0] as string;
      expect(lastHtml).toContain("<strong>");
    });
  });

  it("calls setContent when the content prop changes to a different post", async () => {
    const onChange = vi.fn();
    const { rerender } = render(<Harness initial="<p>First</p>" onChange={onChange} />);
    await waitFor(() => expect(screen.getByText("First")).toBeInTheDocument());

    rerender(
      <Provider theme={defaultTheme}>
        <RichTextEditor content="<p>Second</p>" onChange={onChange} />
      </Provider>,
    );

    await waitFor(() => expect(screen.getByText("Second")).toBeInTheDocument());
    expect(screen.queryByText("First")).not.toBeInTheDocument();
  });
});
