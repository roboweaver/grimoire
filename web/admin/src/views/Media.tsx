import {
  ActionButton,
  Cell,
  Column,
  Content,
  Dialog,
  DialogContainer,
  Divider,
  Flex,
  Grid,
  Heading,
  Image,
  Item,
  Picker,
  Row,
  SearchField,
  TableBody,
  TableHeader,
  TableView,
  Text,
  ToggleButton,
  View,
} from "@adobe/react-spectrum";
import Add from "@spectrum-icons/workflow/Add";
import GridIcon from "@spectrum-icons/workflow/ClassicGridView";
import ImageIcon from "@spectrum-icons/workflow/Image";
import ListIcon from "@spectrum-icons/workflow/ViewList";
import { useState } from "react";
import { useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import { PaginationBar } from "../components/PaginationBar";
import { Empty, ErrorState, Forbidden, Loading } from "../components/States";
import { useAsync } from "../hooks";

export function Media() {
  const [params, setParams] = useSearchParams();
  const page = Math.max(1, Number(params.get("page") || "1") || 1);
  const search = params.get("search") || "";
  const type = params.get("type") || "";
  const after = params.get("after") || "";
  const before = params.get("before") || "";
  const parentId = params.get("parentId") || "";
  const view = params.get("view") === "list" ? "list" : "grid";
  const [reload, setReload] = useState(0);
  const [message, setMessage] = useState<string | null>(null);

  const parentOptionsState = useAsync(
    (signal) => api.posts({ perPage: 100 }, signal),
    [],
  );
  const parentOptions =
    parentOptionsState.status === "success" ? parentOptionsState.data.items : [];
  const parentPickerItems = [
    { key: "all", label: "Any post" },
    ...parentOptions.map((item) => ({
      key: String(item.id),
      label: item.title,
    })),
  ];

  const state = useAsync(
    (signal) =>
      api.media(
        {
          page,
          perPage: 20,
          search: search || undefined,
          type: type || undefined,
          after: after || undefined,
          before: before || undefined,
          parentId: parentId ? Number(parentId) : undefined,
        },
        signal,
      ),
    [page, search, type, after, before, parentId, reload],
  );

  function setFilter(key: string, value: string) {
    setParams((prev) => {
      const p = new URLSearchParams(prev);
      if (value) p.set(key, value);
      else p.delete(key);
      p.set("page", "1");
      return p;
    });
  }

  function setView(next: "grid" | "list") {
    setParams((prev) => {
      const p = new URLSearchParams(prev);
      p.set("view", next);
      return p;
    });
  }

  function goToPage(next: number) {
    setParams((prev) => {
      const p = new URLSearchParams(prev);
      p.set("page", String(next));
      return p;
    });
  }

  async function onPick(ev: React.ChangeEvent<HTMLInputElement>) {
    const file = ev.target.files?.[0];
    if (!file) return;
    try {
      const uploaded = await api.uploadMedia(file);
      setMessage(`Uploaded ${uploaded.filename}`);
      setReload((n) => n + 1);
    } catch (err) {
      setMessage((err as Error).message);
    } finally {
      ev.target.value = "";
    }
  }

  return (
    <Flex direction="column" gap="size-300">
      <Flex justifyContent="space-between" alignItems="center">
        <Heading level={1}>Media</Heading>
        <Flex alignItems="center" gap="size-100">
          <ActionButton
            onPress={() =>
              document.getElementById("media-upload-input")?.click()
            }
          >
            <Add />
            <Text>Upload</Text>
          </ActionButton>
          <input
            id="media-upload-input"
            type="file"
            accept="image/*"
            onChange={onPick}
            style={{ display: "none" }}
          />
        </Flex>
      </Flex>

      <Flex
        direction="row"
        gap="size-200"
        alignItems="end"
        wrap
        justifyContent="space-between"
      >
        <Flex direction="row" gap="size-200" alignItems="end" wrap>
          <SearchField
            label="Search"
            value={search}
            onChange={(v) => setFilter("search", v)}
            width="size-3000"
          />
          <Picker
            label="Type"
            selectedKey={type || "all"}
            onSelectionChange={(key) =>
              setFilter("type", key === "all" ? "" : String(key))
            }
          >
            <Item key="all">All types</Item>
            <Item key="image">Image</Item>
            <Item key="video">Video</Item>
            <Item key="audio">Audio</Item>
            <Item key="document">Document</Item>
          </Picker>
          <label htmlFor="media-after-input">From date</label>
          <input
            id="media-after-input"
            type="date"
            value={after}
            onChange={(ev) => setFilter("after", ev.target.value)}
          />
          <label htmlFor="media-before-input">To date</label>
          <input
            id="media-before-input"
            type="date"
            value={before}
            onChange={(ev) => setFilter("before", ev.target.value)}
          />
          <Picker
            label="Parent post"
            selectedKey={parentId || "all"}
            onSelectionChange={(key) =>
              setFilter("parentId", key === "all" ? "" : String(key))
            }
            items={parentPickerItems}
          >
            {(item) => <Item key={item.key}>{item.label}</Item>}
          </Picker>
        </Flex>
        <Flex gap="size-100">
          <ToggleButton
            isSelected={view === "grid"}
            onChange={() => setView("grid")}
            aria-label="Grid view"
          >
            <GridIcon />
          </ToggleButton>
          <ToggleButton
            isSelected={view === "list"}
            onChange={() => setView("list")}
            aria-label="List view"
          >
            <ListIcon />
          </ToggleButton>
        </Flex>
      </Flex>

      {state.status === "loading" && <Loading label="Loading media" />}
      {state.status === "forbidden" && <Forbidden />}
      {state.status === "error" && <ErrorState message={state.message} />}
      {state.status === "success" &&
        (state.data.items.length === 0 ? (
          <Empty
            heading="No media"
            message="Upload files to build the library, or clear filters."
          />
        ) : (
          <>
            {view === "grid" ? (
              <div data-testid="media-grid-view">
                <Grid
                  columns="repeat(auto-fill, minmax(size-2000, 1fr))"
                  gap="size-200"
                >
                  {state.data.items.map((item) => (
                    <View
                      key={item.id}
                      borderWidth="thin"
                      borderColor="gray-300"
                      borderRadius="medium"
                      padding="size-200"
                    >
                      <Flex direction="column" gap="size-100">
                        {item.mimeType.startsWith("image/") ? (
                          <Image
                            src={item.url}
                            alt={item.title || item.filename}
                            objectFit="cover"
                            height="size-1600"
                          />
                        ) : (
                          <ImageIcon aria-label="Media item" />
                        )}
                        <Text>{item.title || item.filename}</Text>
                        <Text>{item.mimeType}</Text>
                      </Flex>
                    </View>
                  ))}
                </Grid>
              </div>
            ) : (
              <div data-testid="media-list-view">
                <TableView aria-label="Media list">
                  <TableHeader>
                    <Column key="title">Title</Column>
                    <Column key="filename">Filename</Column>
                    <Column key="mime" width={180}>
                      MIME
                    </Column>
                    <Column key="url">URL</Column>
                  </TableHeader>
                  <TableBody>
                    {state.data.items.map((item) => (
                      <Row key={item.id}>
                        <Cell>{item.title || "(untitled)"}</Cell>
                        <Cell>{item.filename}</Cell>
                        <Cell>{item.mimeType}</Cell>
                        <Cell>{item.url}</Cell>
                      </Row>
                    ))}
                  </TableBody>
                </TableView>
              </div>
            )}
            <PaginationBar
              page={state.data.page}
              totalPages={state.data.totalPages}
              total={state.data.total}
              itemLabel="item"
              onPageChange={goToPage}
            />
          </>
        ))}
      <DialogContainer onDismiss={() => setMessage(null)}>
        {message ? (
          <Dialog>
            <Heading>Media upload</Heading>
            <Divider />
            <Content>{message}</Content>
          </Dialog>
        ) : null}
      </DialogContainer>
    </Flex>
  );
}
