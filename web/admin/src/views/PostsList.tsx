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
  StatusLight,
  Item,
  Picker,
  SearchField,
} from "@adobe/react-spectrum";
import Delete from "@spectrum-icons/workflow/Delete";
import Edit from "@spectrum-icons/workflow/Edit";
import { useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import { PaginationBar } from "../components/PaginationBar";
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
  const search = params.get("search") || "";
  const status = params.get("status") || "";
  const author = params.get("author") || "";
  const [reloadToken, setReloadToken] = useState(0);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const authorsState = useAsync((signal) => api.authors(signal), []);
  const authorOptions =
    authorsState.status === "success" ? authorsState.data.authors : [];
  const authorPickerItems = [
    { key: "all", label: "All authors" },
    ...authorOptions.map((item) => ({
      key: String(item.id),
      label: item.displayName,
    })),
  ];

  const state = useAsync(
    (signal) =>
      api.posts(
        {
          page,
          perPage: 10,
          type,
          status: status || undefined,
          search: search || undefined,
          author: author ? Number(author) : undefined,
        },
        signal,
      ),
    [page, type, status, search, author, reloadToken],
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

  function setFilter(key: string, value: string) {
    setParams((prev) => {
      const p = new URLSearchParams(prev);
      if (value) p.set(key, value);
      else p.delete(key);
      p.set("page", "1");
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

      <Flex direction="row" gap="size-200" alignItems="end">
        <SearchField
          label="Search"
          value={search}
          onChange={(v) => setFilter("search", v)}
          width="size-3000"
        />
        <Picker
          label="Status"
          selectedKey={status || "all"}
          onSelectionChange={(key) =>
            setFilter("status", key === "all" ? "" : String(key))
          }
        >
          <Item key="all">All</Item>
          <Item key="publish">Published</Item>
          <Item key="draft">Draft</Item>
          <Item key="pending">Pending</Item>
          <Item key="private">Private</Item>
          <Item key="future">Scheduled</Item>
        </Picker>
        <Picker
          label="Author"
          selectedKey={author || "all"}
          onSelectionChange={(key) =>
            setFilter("author", key === "all" ? "" : String(key))
          }
          items={authorPickerItems}
        >
          {(item) => <Item key={item.key}>{item.label}</Item>}
        </Picker>
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

            <PaginationBar
              page={state.data.page}
              totalPages={state.data.totalPages}
              total={state.data.total}
              itemLabel={label}
              onPageChange={goToPage}
            />
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
