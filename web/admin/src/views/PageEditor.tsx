import { PostEditor } from "./PostEditor";

// PageEditor reuses PostEditor verbatim with type="page" (Req 9.5): pages
// have no categories/tags, which PostEditor already conditions on `type`
// (Req 2's taxonomies are posts-only), so no separate implementation is
// needed here.
export function PageEditor() {
  return <PostEditor type="page" />;
}
