import { Provider, defaultTheme } from "@adobe/react-spectrum";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import type { TermSummary } from "../api/types";
import { TermPicker } from "./TermPicker";

vi.mock("../api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/client")>();
  return {
    ...actual,
    api: { ...actual.api, listTerms: vi.fn(), createTerm: vi.fn() },
  };
});

import { api } from "../api/client";

const listTerms = api.listTerms as unknown as ReturnType<typeof vi.fn>;
const createTerm = api.createTerm as unknown as ReturnType<typeof vi.fn>;

function term(overrides: Partial<TermSummary> = {}): TermSummary {
  return { id: 1, name: "News", slug: "news", ...overrides };
}

function renderPicker(props: { selected: TermSummary[]; onChange: (next: TermSummary[]) => void }) {
  return render(
    <Provider theme={defaultTheme}>
      <TermPicker taxonomy="category" label="Categories" {...props} />
    </Provider>,
  );
}

describe("TermPicker", () => {
  it("lists existing terms fetched from GET /admin/api/terms", async () => {
    listTerms.mockResolvedValue({ items: [term({ id: 1, name: "News" }), term({ id: 2, name: "Sports", slug: "sports" })] });
    renderPicker({ selected: [], onChange: vi.fn() });

    expect(await screen.findByRole("checkbox", { name: "News" })).toBeInTheDocument();
    expect(screen.getByRole("checkbox", { name: "Sports" })).toBeInTheDocument();
    expect(listTerms).toHaveBeenCalledWith("category", expect.anything());
  });

  it("checking an entry adds it to the selection via onChange", async () => {
    listTerms.mockResolvedValue({ items: [term({ id: 1, name: "News" })] });
    const onChange = vi.fn();
    renderPicker({ selected: [], onChange });

    const checkbox = await screen.findByRole("checkbox", { name: "News" });
    await userEvent.click(checkbox);

    expect(onChange).toHaveBeenCalledWith([expect.objectContaining({ id: 1, name: "News" })]);
  });

  it("unchecking a selected entry removes it via onChange", async () => {
    listTerms.mockResolvedValue({ items: [term({ id: 1, name: "News" })] });
    const onChange = vi.fn();
    renderPicker({ selected: [term({ id: 1, name: "News" })], onChange });

    const checkbox = await screen.findByRole("checkbox", { name: "News" });
    expect(checkbox).toBeChecked();
    await userEvent.click(checkbox);

    expect(onChange).toHaveBeenCalledWith([]);
  });

  it("submitting a new term name creates it and adds it to the selection", async () => {
    listTerms.mockResolvedValue({ items: [] });
    createTerm.mockResolvedValue({ id: 9, name: "Weather", slug: "weather", taxonomy: "category" });

    // A controlled harness mirrors real usage (PostEditor holds the
    // `selected` state and re-renders TermPicker with the parent's updated
    // value) so this test can assert the newly-created term ends up checked,
    // not just that onChange fired with the right payload.
    function Harness() {
      const [selected, setSelected] = useState<TermSummary[]>([]);
      return <TermPicker taxonomy="category" label="Categories" selected={selected} onChange={setSelected} />;
    }
    render(
      <Provider theme={defaultTheme}>
        <Harness />
      </Provider>,
    );

    const input = await screen.findByRole("textbox", { name: /new category/i });
    await userEvent.type(input, "Weather");
    await userEvent.click(screen.getByRole("button", { name: /add/i }));

    await waitFor(() =>
      expect(createTerm).toHaveBeenCalledWith(
        expect.objectContaining({ name: "Weather", slug: "weather", taxonomy: "category" }),
      ),
    );
    expect(await screen.findByRole("checkbox", { name: "Weather" })).toBeChecked();
  });
});
