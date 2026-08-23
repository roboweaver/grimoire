import {
  Heading,
  Flex,
  TableView,
  TableHeader,
  TableBody,
  Column,
  Row,
  Cell,
  ActionButton,
  Text,
  StatusLight,
} from "@adobe/react-spectrum";
import ChevronLeft from "@spectrum-icons/workflow/ChevronLeft";
import ChevronRight from "@spectrum-icons/workflow/ChevronRight";
import { useNavigate, useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import { useAsync } from "../hooks";
import { Empty, ErrorState, Forbidden, Loading } from "../components/States";

const STATUS_VARIANT: Record<
  string,
  "positive" | "neutral" | "notice" | "info"
> = {
  publish: "positive",
  draft: "neutral",
  pending: "notice",
  private: "info",
  future: "info",
};

// PostsList renders the paginated content listing (incl. drafts and pages) from
// GET /admin/api/posts (Req 5, 8.2). Page state lives in the URL query so SPA
// navigation and reloads are stable.
export function PostsList() {
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const page = Math.max(1, Number(params.get("page") || "1") || 1);

  const state = useAsync(
    (signal) => api.posts({ page, perPage: 10 }, signal),
    [page],
  );

  function goToPage(next: number) {
    setParams((prev) => {
      const p = new URLSearchParams(prev);
      p.set("page", String(next));
      return p;
    });
  }

  return (
    <Flex direction="column" gap="size-300">
      <Heading level={1}>Content</Heading>

      {state.status === "loading" && <Loading label="Loading content" />}
      {state.status === "forbidden" && <Forbidden />}
      {state.status === "error" && <ErrorState message={state.message} />}
      {state.status === "success" &&
        (state.data.items.length === 0 ? (
          <Empty
            heading="No content"
            message="Nothing has been published or drafted yet."
          />
        ) : (
          <>
            <TableView
              aria-label="Posts and pages"
              onAction={(key) => navigate(`/posts/${key}`)}
              density="spacious"
            >
              <TableHeader>
                <Column key="title">Title</Column>
                <Column key="type" width={120}>
                  Type
                </Column>
                <Column key="status" width={140}>
                  Status
                </Column>
                <Column key="date" width={200}>
                  Date
                </Column>
              </TableHeader>
              <TableBody>
                {state.data.items.map((item) => (
                  <Row key={item.id}>
                    <Cell>{item.title || "(untitled)"}</Cell>
                    <Cell>{item.type}</Cell>
                    <Cell>
                      <StatusLight
                        variant={STATUS_VARIANT[item.status] ?? "neutral"}
                      >
                        {item.status}
                      </StatusLight>
                    </Cell>
                    <Cell>{formatDate(item.date)}</Cell>
                  </Row>
                ))}
              </TableBody>
            </TableView>

            <Flex
              direction="row"
              alignItems="center"
              justifyContent="space-between"
            >
              <Text>
                Page {state.data.page} of {Math.max(1, state.data.totalPages)} ·{" "}
                {state.data.total} item{state.data.total === 1 ? "" : "s"}
              </Text>
              <Flex gap="size-100">
                <ActionButton
                  isDisabled={page <= 1}
                  onPress={() => goToPage(page - 1)}
                  aria-label="Previous page"
                >
                  <ChevronLeft />
                  <Text>Previous</Text>
                </ActionButton>
                <ActionButton
                  isDisabled={page >= state.data.totalPages}
                  onPress={() => goToPage(page + 1)}
                  aria-label="Next page"
                >
                  <Text>Next</Text>
                  <ChevronRight />
                </ActionButton>
              </Flex>
            </Flex>
          </>
        ))}
    </Flex>
  );
}

function formatDate(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}
