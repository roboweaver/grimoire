import {
  Heading,
  Flex,
  View,
  Text,
  StatusLight,
  ActionButton,
  Content,
  Well,
  Divider,
} from "@adobe/react-spectrum";
import ChevronLeft from "@spectrum-icons/workflow/ChevronLeft";
import { useNavigate, useParams } from "react-router-dom";
import { api } from "../api/client";
import { useAsync } from "../hooks";
import { ErrorState, Forbidden, Loading } from "../components/States";

// PostDetail shows a single item's read-only detail from
// GET /admin/api/posts/{id} (Req 6, 8.2). Editing arrives in milestone 06.
export function PostDetail() {
  const { id = "" } = useParams();
  const navigate = useNavigate();
  const state = useAsync((signal) => api.post(id, signal), [id]);

  return (
    <Flex direction="column" gap="size-300">
      <ActionButton
        isQuiet
        alignSelf="start"
        onPress={() => navigate("/posts")}
        aria-label="Back to content"
      >
        <ChevronLeft />
        <Text>Back to content</Text>
      </ActionButton>

      {state.status === "loading" && <Loading label="Loading item" />}
      {state.status === "forbidden" && <Forbidden />}
      {state.status === "error" && <ErrorState message={state.message} />}
      {state.status === "success" && (
        <Flex direction="column" gap="size-200">
          <Heading level={1} margin={0}>
            {state.data.title || "(untitled)"}
          </Heading>

          <Flex gap="size-200" alignItems="center" wrap>
            <StatusLight
              variant={state.data.status === "publish" ? "positive" : "neutral"}
            >
              {state.data.status}
            </StatusLight>
            <Text>{state.data.type}</Text>
            <Text>/{state.data.slug}</Text>
            <Text>{formatDate(state.data.date)}</Text>
          </Flex>

          <Divider size="S" />

          {state.data.excerpt ? (
            <View>
              <Heading level={3}>Excerpt</Heading>
              <Content>{state.data.excerpt}</Content>
            </View>
          ) : null}

          <View>
            <Heading level={3}>Content</Heading>
            {state.data.content ? (
              <Well>
                {/*
                  Read-only view: render the stored post_content as text, not
                  HTML. The API returns it as data and React escapes it; a
                  rendered/sandboxed preview is deferred to milestone 06's editor
                  (design.md, Security considerations).
                */}
                <View
                  UNSAFE_style={{ whiteSpace: "pre-wrap", wordBreak: "break-word" }}
                >
                  {state.data.content}
                </View>
              </Well>
            ) : (
              <Text>No content.</Text>
            )}
          </View>
        </Flex>
      )}
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
