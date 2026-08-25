import { Provider, defaultTheme } from "@adobe/react-spectrum";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ConflictDialog } from "./ConflictDialog";

function renderDialog(props: Partial<React.ComponentProps<typeof ConflictDialog>> = {}) {
  return render(
    <Provider theme={defaultTheme}>
      <ConflictDialog
        isOpen
        currentModified="2024-03-01T12:00:00Z"
        onReloadLatest={vi.fn()}
        onKeepEditing={vi.fn()}
        {...props}
      />
    </Provider>,
  );
}

describe("ConflictDialog", () => {
  it("renders with the current-modified timestamp when open", () => {
    renderDialog({ currentModified: "2024-03-01T12:00:00Z" });
    expect(screen.getByText(/changed since you loaded it/i)).toBeInTheDocument();
    expect(screen.getByText(/2024-03-01T12:00:00Z/)).toBeInTheDocument();
  });

  it("does not render when closed", () => {
    renderDialog({ isOpen: false });
    expect(screen.queryByText(/changed since you loaded it/i)).not.toBeInTheDocument();
  });

  it("calls onReloadLatest when 'Reload latest' is pressed", async () => {
    const onReloadLatest = vi.fn();
    renderDialog({ onReloadLatest });

    await userEvent.click(screen.getByRole("button", { name: /reload latest/i }));

    expect(onReloadLatest).toHaveBeenCalledTimes(1);
  });

  it("calls onKeepEditing (without discarding edits) when 'Keep editing' is pressed", async () => {
    const onKeepEditing = vi.fn();
    const onReloadLatest = vi.fn();
    renderDialog({ onKeepEditing, onReloadLatest });

    await userEvent.click(screen.getByRole("button", { name: /keep editing/i }));

    expect(onKeepEditing).toHaveBeenCalledTimes(1);
    expect(onReloadLatest).not.toHaveBeenCalled();
  });
});
