import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { renderWithSpectrum } from "../test-utils";
import { PaginationBar } from "./PaginationBar";

describe("PaginationBar", () => {
  it("shows the page/total-pages/item-count summary", () => {
    renderWithSpectrum(
      <PaginationBar page={2} totalPages={5} total={42} itemLabel="item" onPageChange={vi.fn()} />,
    );
    expect(screen.getByText("Page 2 of 5 · 42 items")).toBeInTheDocument();
  });

  it("disables Previous on page 1 and Next on the last page", () => {
    renderWithSpectrum(
      <PaginationBar page={1} totalPages={1} total={3} itemLabel="post" onPageChange={vi.fn()} />,
    );
    expect(screen.getByRole("button", { name: /previous page/i })).toBeDisabled();
    expect(screen.getByRole("button", { name: /next page/i })).toBeDisabled();
  });

  it("disables Next but keeps Previous enabled on the last page when there are multiple pages", () => {
    renderWithSpectrum(
      <PaginationBar page={5} totalPages={5} total={50} itemLabel="item" onPageChange={vi.fn()} />,
    );
    expect(screen.getByRole("button", { name: /previous page/i })).not.toBeDisabled();
    expect(screen.getByRole("button", { name: /next page/i })).toBeDisabled();
  });

  it("calls onPageChange with page +/- 1", async () => {
    const user = userEvent.setup();
    const onPageChange = vi.fn();
    renderWithSpectrum(
      <PaginationBar page={2} totalPages={5} total={42} itemLabel="item" onPageChange={onPageChange} />,
    );
    await user.click(screen.getByRole("button", { name: /previous page/i }));
    await user.click(screen.getByRole("button", { name: /next page/i }));
    expect(onPageChange).toHaveBeenNthCalledWith(1, 1);
    expect(onPageChange).toHaveBeenNthCalledWith(2, 3);
  });

  it("treats a 0-item, 0-page result as showing 1 disabled page (zero-post empty state, Req 3.4/8.1)", () => {
    renderWithSpectrum(
      <PaginationBar page={1} totalPages={0} total={0} itemLabel="item" onPageChange={vi.fn()} />,
    );
    expect(screen.getByText("Page 1 of 1 · 0 items")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /next page/i })).toBeDisabled();
  });
});
