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
  Button,
  DialogTrigger,
  AlertDialog,
  Text,
  StatusLight,
} from "@adobe/react-spectrum";
import ChevronLeft from "@spectrum-icons/workflow/ChevronLeft";
import ChevronRight from "@spectrum-icons/workflow/ChevronRight";
import Delete from "@spectrum-icons/workflow/Delete";
import Edit from "@spectrum-icons/workflow/Edit";
import { useState } from "react";
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

interface PostsListProps {
  type?: "post" | "page";
}

// PostsList renders the paginated content listing (incl. drafts and pages) from
// GET /admin/api/posts (Req 5, 8.2). Page state lives in the URL query so SPA
// navigation and reloads are stable. `type` filters to posts or pages
// (task 4.12); the "/pages" route passes type="page", "/posts" passes
// type="post".
export function PostsList({ type = "post" }: PostsListProps) {
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const page = Math.max(1, Number(params.get("page") || "1") || 1);
  const [reloadToken, setReloadToken] = useState(0);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const state = useAsync(
    (signal) => api.posts({ page, perPage: 10, type }, signal),
    [page, type, reloadToken],
  );

  const basePath = type === "page" ? "/pages" : "/posts";
  const label = type === "page" ? "page" : "post";

  function goToPage(next: number) {
    setParams((prev) => {
      const p = new URLSearchParams(prev);
      p.set("page", String(next));
      return p;
    });
  }

  async function handleDelete(id: number) {
    setDeleteError(null);
    try {
      await api.deletePost(id);
      setReloadToken((t) => t + 1);
    } catch (err) {
      setDeleteError(err instanceof Error ? err.message : "Delete failed.");
    }
  }

  return (
    <Flex direction="column" gap="size-300">
      <Flex direction="row" justifyContent="space-between" alignItems="center">
        <Heading level={1} margin={0}>
          {type === "page" ? "Pages" : "Content"}
        </Heading>
        <Button variant="accent" onPress={() => navigate(`${basePath}/new`)}>
          New {label}
        </Button>
      </Flex>

      {deleteError && <ErrorState message={deleteError} />}

      {state.status === "loading" && <Loading label="Loading content" />}
      {state.status === "forbidden" && <Forbidden />}
      {state.status === "error" && <ErrorState message={state.message} />}
      {state.status === "success" &&
        (state.data.items.length === 0 ? (
          <Empty
            heading={`No ${label}s`}
            message="Nothing has been published or drafted yet."
          />
        ) : (
          <>
            <TableView
              aria-label={type === "page" ? "Pages" : "Posts and pages"}
              density="spacious"
            >
              <TableHeader>
                <Column key="title">Title</Column>
                <Column key="status" width={140}>
                  Status
                </Column>
                <Column key="date" width={200}>
                  Date
                </Column>
                <Column key="actions" width={140} align="end">
                  Actions
                </Column>
              </TableHeader>
              <TableBody>
                {state.data.items.map((item) => (
                  <Row key={item.id}>
                    <Cell>{item.title || "(untitled)"}</Cell>
                    <Cell>
                      <StatusLight
                        variant={STATUS_VARIANT[item.status] ?? "neutral"}
                      >
                        {item.status}
                      </StatusLight>
                    </Cell>
                    <Cell>{formatDate(item.date)}</Cell>
                    <Cell>
                      <Flex gap="size-100" justifyContent="end">
                        <ActionButton
                          isQuiet
                          aria-label={`Edit ${item.title || "item"}`}
                          onPress={() => navigate(`${basePath}/${item.id}`)}
                        >
                          <Edit />
                        </ActionButton>
                        <DialogTrigger>
                          <ActionButton
                            isQuiet
                            aria-label={`Delete ${item.title || "item"}`}
                          >
                            <Delete />
                          </ActionButton>
                          {(close) => (
                            <AlertDialog
                              title={`Delete this ${label}?`}
                              variant="destructive"
                              primaryActionLabel="Delete"
                              cancelLabel="Cancel"
                              onPrimaryAction={() => {
                                void handleDelete(item.id);
                                close();
                              }}
                            >
                              This cannot be undone.
                            </AlertDialog>
                          )}
                        </DialogTrigger>
                      </Flex>
                    </Cell>
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
