import { ActionButton, Cell, Column, Flex, Heading, Picker, Item, Row, TableBody, TableHeader, TableView, Text } from "@adobe/react-spectrum";
import { useMemo, useState } from "react";
import { api } from "../api/client";
import { Empty, ErrorState, Forbidden, Loading } from "../components/States";
import { useAsync } from "../hooks";

const ACTIONS: Record<string, string[]> = {
  pending: ["approve", "spam", "trash"],
  approved: ["unapprove", "spam", "trash"],
  spam: ["not-spam", "trash"],
  trash: ["untrash"],
};

export function Comments() {
  const [status, setStatus] = useState<string>("all");
  const [reload, setReload] = useState(0);
  const state = useAsync((signal) => api.comments({ status: status === "all" ? undefined : status }, signal), [status, reload]);

  async function act(id: number, next: string) {
    await api.moderateComment(id, next);
    setReload((n) => n + 1);
  }

  const items = state.status === "success" ? state.data.items : [];
  const total = state.status === "success" ? state.data.total : 0;
  const label = useMemo(() => `${total} comment${total === 1 ? "" : "s"}`, [total]);

  return (
    <Flex direction="column" gap="size-300">
      <Heading level={1}>Comments</Heading>
      <Flex justifyContent="space-between" alignItems="end">
        <Picker label="Status" selectedKey={status} onSelectionChange={(key) => setStatus(String(key))}>
          <Item key="all">All</Item>
          <Item key="pending">Pending</Item>
          <Item key="approved">Approved</Item>
          <Item key="spam">Spam</Item>
          <Item key="trash">Trash</Item>
        </Picker>
        <Text>{label}</Text>
      </Flex>
      {state.status === "loading" && <Loading label="Loading comments" />}
      {state.status === "forbidden" && <Forbidden />}
      {state.status === "error" && <ErrorState message={state.message} />}
      {state.status === "success" && (items.length === 0 ? <Empty heading="No comments" message="No comments match this filter." /> : (
        <TableView aria-label="Comments moderation table">
          <TableHeader>
            <Column key="author">Author</Column>
            <Column key="post">Post</Column>
            <Column key="status" width={120}>Status</Column>
            <Column key="excerpt">Excerpt</Column>
            <Column key="actions" width={240}>Actions</Column>
          </TableHeader>
          <TableBody>
            {items.map((item) => (
              <Row key={item.id}>
                <Cell>{item.author || item.authorEmail}</Cell>
                <Cell>{item.postTitle || `Post #${item.postId}`}</Cell>
                <Cell>{item.status}</Cell>
                <Cell>{item.excerpt || item.content}</Cell>
                <Cell>
                  <Flex gap="size-100" wrap>
                    {(ACTIONS[item.status] || []).map((action) => (
                      <ActionButton key={action} onPress={() => act(item.id, action)}>{action}</ActionButton>
                    ))}
                  </Flex>
                </Cell>
              </Row>
            ))}
          </TableBody>
        </TableView>
      ))}
    </Flex>
  );
}
