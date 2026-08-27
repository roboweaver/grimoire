import { ActionButton, Flex, Text } from "@adobe/react-spectrum";
import ChevronLeft from "@spectrum-icons/workflow/ChevronLeft";
import ChevronRight from "@spectrum-icons/workflow/ChevronRight";

export interface PaginationBarProps {
  /** 1-based current page. */
  page: number;
  /** From the shared Page/TotalPages contract (Task 1); 0 means an empty result set. */
  totalPages: number;
  total: number;
  /** Singular noun pluralized with a trailing "s" (e.g. "item", "post"). */
  itemLabel: string;
  onPageChange: (next: number) => void;
}

// PaginationBar is the one pagination control used by every admin list (Req
// 8.1). It renders "Page X of Y · N items" plus Previous/Next controls that
// disable at the ends. A 0-item result (totalPages === 0) displays as "Page 1
// of 1" rather than "Page 1 of 0" so a genuinely empty list still reads as a
// complete, non-broken page.
export function PaginationBar({ page, totalPages, total, itemLabel, onPageChange }: PaginationBarProps) {
  const displayTotalPages = Math.max(1, totalPages);
  return (
    <Flex direction="row" alignItems="center" justifyContent="space-between">
      <Text>
        Page {page} of {displayTotalPages} ·{" "}
        {total} {itemLabel}
        {total === 1 ? "" : "s"}
      </Text>
      <Flex gap="size-100">
        <ActionButton isDisabled={page <= 1} onPress={() => onPageChange(page - 1)} aria-label="Previous page">
          <ChevronLeft />
          <Text>Previous</Text>
        </ActionButton>
        <ActionButton isDisabled={page >= displayTotalPages} onPress={() => onPageChange(page + 1)} aria-label="Next page">
          <Text>Next</Text>
          <ChevronRight />
        </ActionButton>
      </Flex>
    </Flex>
  );
}
