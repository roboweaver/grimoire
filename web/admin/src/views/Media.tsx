import { ActionButton, Cell, Column, Dialog, DialogContainer, Content, Divider, Flex, Grid, Heading, Image, Row, TableBody, TableHeader, TableView, Text, View } from "@adobe/react-spectrum";
import Add from "@spectrum-icons/workflow/Add";
import ImageIcon from "@spectrum-icons/workflow/Image";
import { useState } from "react";
import { api } from "../api/client";
import { Empty, ErrorState, Forbidden, Loading } from "../components/States";
import { useAsync } from "../hooks";

export function Media() {
  const [reload, setReload] = useState(0);
  const [message, setMessage] = useState<string | null>(null);
  const state = useAsync((signal) => api.media({}, signal), [reload]);

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
          <ActionButton onPress={() => document.getElementById("media-upload-input")?.click()}><Add /><Text>Upload</Text></ActionButton>
          <input id="media-upload-input" type="file" accept="image/*" onChange={onPick} style={{ display: "none" }} />
        </Flex>
      </Flex>
      {state.status === "loading" && <Loading label="Loading media" />}
      {state.status === "forbidden" && <Forbidden />}
      {state.status === "error" && <ErrorState message={state.message} />}
      {state.status === "success" && (state.data.items.length === 0 ? <Empty heading="No media" message="Upload files to build the library." /> : (
        <>
          <Grid columns="repeat(auto-fill, minmax(size-2000, 1fr))" gap="size-200">
            {state.data.items.map((item) => (
              <View key={item.id} borderWidth="thin" borderColor="gray-300" borderRadius="medium" padding="size-200">
                <Flex direction="column" gap="size-100">
                  {item.mimeType.startsWith("image/") ? (
                    <Image src={item.url} alt={item.title || item.filename} objectFit="cover" height="size-1600" />
                  ) : (
                    <ImageIcon aria-label="Media item" />
                  )}
                  <Text>{item.title || item.filename}</Text>
                  <Text>{item.mimeType}</Text>
                </Flex>
              </View>
            ))}
          </Grid>
          <TableView aria-label="Media list">
            <TableHeader>
              <Column key="title">Title</Column>
              <Column key="filename">Filename</Column>
              <Column key="mime" width={180}>MIME</Column>
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
