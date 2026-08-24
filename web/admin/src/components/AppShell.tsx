import {
  Grid,
  View,
  Flex,
  Heading,
  Text,
  Link as SpectrumLink,
} from "@adobe/react-spectrum";
import { Outlet, useNavigate, useLocation } from "react-router-dom";
import type { SessionInfo } from "../api/types";

const NAV = [
  { label: "Dashboard", path: "/" },
  { label: "Content", path: "/posts" },
  { label: "Comments", path: "/comments" },
  { label: "Media", path: "/media" },
  { label: "Menus", path: "/menus" },
];

// AppShell is the persistent chrome: a Spectrum header, a simple nav rail, and
// the routed content area. Layout uses Spectrum Grid + dimension tokens only.
export function AppShell({ session }: { session: SessionInfo }) {
  const navigate = useNavigate();
  const location = useLocation();

  return (
    <Grid
      areas={["header header", "nav main"]}
      columns={["size-2400", "1fr"]}
      rows={["size-800", "1fr"]}
      minHeight="100vh"
    >
      <View
        gridArea="header"
        backgroundColor="gray-100"
        borderBottomWidth="thin"
        borderBottomColor="gray-300"
        paddingX="size-300"
      >
        <Flex
          height="100%"
          alignItems="center"
          justifyContent="space-between"
        >
          <Heading level={3} margin={0}>
            Grimoire Admin
          </Heading>
          <Text>
            {session.displayName || session.login}
          </Text>
        </Flex>
      </View>

      <View
        gridArea="nav"
        backgroundColor="gray-75"
        borderEndWidth="thin"
        borderEndColor="gray-300"
        padding="size-200"
      >
        <Flex direction="column" gap="size-100">
          {NAV.map((item) => {
            const active =
              item.path === "/"
                ? location.pathname === "/"
                : location.pathname.startsWith(item.path);
            return (
              <SpectrumLink
                key={item.path}
                isQuiet
                variant={active ? "primary" : "secondary"}
                onPress={() => navigate(item.path)}
              >
                {item.label}
              </SpectrumLink>
            );
          })}
        </Flex>
      </View>

      <View gridArea="main" padding="size-400" overflow="auto">
        <Outlet />
      </View>
    </Grid>
  );
}
