import { Flex, Heading, Text, View } from "@adobe/react-spectrum";
import { api } from "../api/client";
import { Empty, ErrorState, Forbidden, Loading } from "../components/States";
import { useAsync } from "../hooks";
import type { NavMenuItem } from "../api/types";

function Tree({ items }: { items: NavMenuItem[] }) {
  if (!items.length) return null;
  return (
    <ul>
      {items.map((item) => (
        <li key={item.id}>
          <Text>{item.label} — {item.url}</Text>
          <Tree items={item.children || []} />
        </li>
      ))}
    </ul>
  );
}

export function Menus() {
  const state = useAsync((signal) => api.menus(signal), []);
  return (
    <Flex direction="column" gap="size-300">
      <Heading level={1}>Menus</Heading>
      <Text>Menu editing is deferred to a later milestone. This view is read-only.</Text>
      {state.status === "loading" && <Loading label="Loading menus" />}
      {state.status === "forbidden" && <Forbidden />}
      {state.status === "error" && <ErrorState message={state.message} />}
      {state.status === "success" && (state.data.items.length === 0 ? <Empty heading="No menus" message="No menus are configured." /> : (
        <Flex direction="column" gap="size-300">
          {state.data.items.map((menu) => (
            <View key={menu.id} borderWidth="thin" borderColor="gray-300" borderRadius="medium" padding="size-200">
              <Heading level={3}>{menu.name}</Heading>
              <Tree items={menu.items} />
            </View>
          ))}
        </Flex>
      ))}
    </Flex>
  );
}
