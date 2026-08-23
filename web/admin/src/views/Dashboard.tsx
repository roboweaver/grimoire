import {
  Heading,
  Grid,
  View,
  Flex,
  Text,
} from "@adobe/react-spectrum";
import { api } from "../api/client";
import { useAsync } from "../hooks";
import { ErrorState, Forbidden, Loading } from "../components/States";

function StatCard({ label, value }: { label: string; value: number }) {
  return (
    <View
      backgroundColor="gray-50"
      borderWidth="thin"
      borderColor="gray-300"
      borderRadius="medium"
      padding="size-300"
    >
      <Flex direction="column" gap="size-100">
        <Text>{label}</Text>
        <Heading level={2} margin={0}>
          {value}
        </Heading>
      </Flex>
    </View>
  );
}

// Dashboard shows the content counts from GET /admin/api/stats (Req 4, 8.2).
export function Dashboard() {
  const state = useAsync((signal) => api.stats(signal), []);

  return (
    <Flex direction="column" gap="size-300">
      <Heading level={1}>Dashboard</Heading>
      {state.status === "loading" && <Loading label="Loading dashboard" />}
      {state.status === "forbidden" && <Forbidden />}
      {state.status === "error" && <ErrorState message={state.message} />}
      {state.status === "success" && (
        <Grid
          columns="repeat(auto-fill, minmax(size-2400, 1fr))"
          gap="size-300"
        >
          <StatCard label="Published posts" value={state.data.posts.published} />
          <StatCard label="Draft posts" value={state.data.posts.draft} />
          <StatCard label="Pages" value={state.data.pages} />
          <StatCard label="Categories" value={state.data.categories} />
          <StatCard label="Users" value={state.data.users} />
        </Grid>
      )}
    </Flex>
  );
}
