import { Routes, Route } from "react-router-dom";
import { api } from "./api/client";
import { useAsync } from "./hooks";
import { AppShell } from "./components/AppShell";
import { ErrorState, Loading } from "./components/States";
import { Dashboard } from "./views/Dashboard";
import { PostsList } from "./views/PostsList";
import { PostDetail } from "./views/PostDetail";

// App loads the current session first. A 401 redirects to /login inside the API
// client, so here we only handle loading and hard errors before mounting the
// authenticated shell and routes.
export function App() {
  const state = useAsync((signal) => api.session(signal), []);

  if (state.status === "loading") {
    return <Loading label="Loading admin" />;
  }
  if (state.status === "error") {
    return <ErrorState message={state.message} />;
  }
  if (state.status === "forbidden") {
    // The session endpoint requires only login, so this is unexpected; surface
    // it rather than render a blank screen.
    return <ErrorState message="Unable to load your session." />;
  }

  return (
    <Routes>
      <Route element={<AppShell session={state.data} />}>
        <Route index element={<Dashboard />} />
        <Route path="posts" element={<PostsList />} />
        <Route path="posts/:id" element={<PostDetail />} />
        <Route path="*" element={<Dashboard />} />
      </Route>
    </Routes>
  );
}
