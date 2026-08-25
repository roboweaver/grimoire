import {
  ActionButton,
  AlertDialog,
  Button,
  ButtonGroup,
  DatePicker,
  DialogTrigger,
  Flex,
  Heading,
  Item,
  Picker,
  Switch,
  Text,
  TextArea,
  TextField,
  View,
} from "@adobe/react-spectrum";
import { parseAbsoluteToLocal, type ZonedDateTime } from "@internationalized/date";
import ChevronLeft from "@spectrum-icons/workflow/ChevronLeft";
import { useEffect, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { api, ConflictError, ForbiddenError } from "../api/client";
import type { PostDetail, PostWriteInput, TermSummary } from "../api/types";
import { ConflictDialog } from "../components/ConflictDialog";
import { RevisionsPanel } from "../components/RevisionsPanel";
import { RichTextEditor } from "../components/RichTextEditor";
import { TermPicker } from "../components/TermPicker";
import { ErrorState, Forbidden, Loading } from "../components/States";
import { useAutosave } from "../useAutosave";

const STATUS_OPTIONS = ["draft", "pending", "publish", "private", "future"] as const;

interface PostEditorProps {
  type: "post" | "page";
}

// PostEditor is the create/edit view for both posts and pages (Req 9.2, 9.5);
// PageEditor.tsx is a thin wrapper passing type="page" and hiding the
// category/tag picker (Req 2's taxonomies apply to posts only).
//
// Req 9.2's field list is title, rich-text body, excerpt, status picker,
// slug, comment-status toggle, category/tag picker (posts only), Save/
// Delete. Requirement 1's "M6 is the first milestone to expose all five
// [statuses] from the editor" means `future` must be selectable here, which
// in turn means the editor needs a schedule date field: the admin API
// rejects a `future`-status write with no (or a non-future) date (Req 5.2).
// The scheduleDate field below is therefore only shown/sent when status is
// "future"; for every other status, `date` is omitted from the write body
// entirely, which preserves the post's existing stored date unchanged
// (content.PostWriteService.Update only overwrites Date when non-zero) and
// lets Create default it to "now" (matching WordPress's own behavior).
export function PostEditor({ type }: PostEditorProps) {
  const { id } = useParams();
  const navigate = useNavigate();
  const isNew = id === undefined;
  const listPath = type === "post" ? "/posts" : "/pages";

  const [loadState, setLoadState] = useState<"loading" | "forbidden" | "error" | "ready">(
    isNew ? "ready" : "loading",
  );
  const [loadError, setLoadError] = useState("");

  const [title, setTitle] = useState("");
  const [slug, setSlug] = useState("");
  const [excerpt, setExcerpt] = useState("");
  const [content, setContent] = useState("");
  const [status, setStatus] = useState<string>("draft");
  const [commentsOpen, setCommentsOpen] = useState(true);
  const [categories, setCategories] = useState<TermSummary[]>([]);
  const [tags, setTags] = useState<TermSummary[]>([]);
  const [modified, setModified] = useState<string | undefined>(undefined);
  // scheduleDate only matters (and is only rendered) for status:"future";
  // it starts undefined so an unmodified existing future post's date is
  // sent through unchanged (Req 5.2's exemption), not clobbered with "now".
  const [scheduleDate, setScheduleDate] = useState<ZonedDateTime | null>(null);

  const [isSaving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [conflict, setConflict] = useState<{ currentModified: string } | null>(null);

  // savedSnapshot tracks the last loaded/saved title+content+excerpt, so
  // isDirty (below) can tell whether the editor currently has unsaved
  // changes without a separate "dirty" flag to keep in sync by hand.
  const [savedSnapshot, setSavedSnapshot] = useState({ title: "", content: "", excerpt: "" });
  const isDirty = title !== savedSnapshot.title || content !== savedSnapshot.content || excerpt !== savedSnapshot.excerpt;

  const { notice: autosaveNotice, dismissNotice } = useAutosave({
    postId: isNew ? undefined : id,
    postModified: modified,
    isDirty,
    getSnapshot: () => ({ title, content, excerpt }),
  });

  function applyLoaded(detail: PostDetail) {
    setTitle(detail.title);
    setSlug(detail.slug);
    setExcerpt(detail.excerpt);
    setContent(detail.content);
    setStatus(detail.status);
    setCommentsOpen(detail.commentStatus !== "closed");
    setCategories(detail.terms?.category ?? []);
    setTags(detail.terms?.post_tag ?? []);
    setModified(detail.modified);
    setSavedSnapshot({ title: detail.title, content: detail.content, excerpt: detail.excerpt });
    // Only preload scheduleDate when the post was ALREADY future-status: the
    // server's unchanged-date exemption (Req 5.2) only applies to a post
    // that stays future, so preloading a past date here for a draft/
    // published post would silently show a value that fails validation the
    // moment the user flips status to "future" without touching the picker.
    // Leaving it empty in that case forces an explicit (and valid) date pick.
    setScheduleDate(detail.status === "future" && detail.date ? parseAbsoluteToLocal(detail.date) : null);
  }

  function load(signal?: AbortSignal) {
    if (isNew || !id) return;
    setLoadState("loading");
    api
      .post(id, signal)
      .then((detail) => {
        applyLoaded(detail);
        setLoadState("ready");
      })
      .catch((err: unknown) => {
        if (signal?.aborted) return;
        if (err instanceof ForbiddenError) {
          setLoadState("forbidden");
          return;
        }
        setLoadError(err instanceof Error ? err.message : "Something went wrong.");
        setLoadState("error");
      });
  }

  useEffect(() => {
    const ctrl = new AbortController();
    load(ctrl.signal);
    return () => ctrl.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id]);

  async function handleSave() {
    setSaving(true);
    setSaveError(null);
    const body: PostWriteInput = {
      title,
      content,
      excerpt,
      slug,
      status,
      type,
      commentStatus: commentsOpen ? "open" : "closed",
      // Only send date for a scheduled ("future") post that actually has a
      // schedule date set; every other status omits it so the backend's
      // unchanged-on-update / defaulted-on-create semantics apply.
      ...(status === "future" && scheduleDate ? { date: scheduleDate.toDate().toISOString() } : {}),
      ...(isNew ? {} : { modified }),
      ...(type === "post" ? { termIds: { category: categories.map((c) => c.id), post_tag: tags.map((t) => t.id) } } : {}),
    };
    try {
      const saved = isNew ? await api.createPost(body) : await api.updatePost(id!, body);
      applyLoaded(saved);
      if (isNew) {
        navigate(`${listPath}/${saved.id}`, { replace: true });
      }
    } catch (err) {
      if (err instanceof ConflictError) {
        setConflict({ currentModified: err.currentModified });
        return;
      }
      if (err instanceof ForbiddenError) {
        setSaveError("Your account doesn't have permission to do that.");
        return;
      }
      setSaveError(err instanceof Error ? err.message : "Something went wrong.");
    } finally {
      setSaving(false);
    }
  }

  async function handleDelete() {
    if (!id) return;
    await api.deletePost(id);
    navigate(listPath);
  }

  function reloadLatest() {
    setConflict(null);
    load();
  }

  if (loadState === "loading") return <Loading label="Loading item" />;
  if (loadState === "forbidden") return <Forbidden />;
  if (loadState === "error") return <ErrorState message={loadError} />;

  const kind = type === "post" ? "post" : "page";

  return (
    <Flex direction="column" gap="size-300">
      <ActionButton isQuiet alignSelf="start" onPress={() => navigate(listPath)} aria-label="Back to content">
        <ChevronLeft />
        <Text>Back to content</Text>
      </ActionButton>

      <Heading level={1} margin={0}>
        {isNew ? `New ${kind}` : `Edit ${kind}`}
      </Heading>

      {saveError ? (
        <View backgroundColor="negative" borderRadius="medium" padding="size-150">
          <Text>{saveError}</Text>
        </View>
      ) : null}

      {autosaveNotice ? (
        <View backgroundColor="notice" borderRadius="medium" padding="size-150">
          <Flex gap="size-150" alignItems="center" wrap>
            <Text>
              An autosaved draft from {formatDate(autosaveNotice.modified)} is newer than this {kind}.
            </Text>
            <Button
              variant="secondary"
              onPress={() => {
                setTitle(autosaveNotice.title);
                setContent(autosaveNotice.content);
                setExcerpt(autosaveNotice.excerpt);
                dismissNotice();
              }}
            >
              Load autosave
            </Button>
            <ActionButton isQuiet onPress={dismissNotice} aria-label="Dismiss autosave notice">
              Dismiss
            </ActionButton>
          </Flex>
        </View>
      ) : null}

      <TextField label="Title" value={title} onChange={setTitle} width="100%" />
      <TextField label="Slug" value={slug} onChange={setSlug} width="100%" />
      <TextArea label="Excerpt" value={excerpt} onChange={setExcerpt} width="100%" />

      <View>
        <Heading level={4}>Content</Heading>
        <RichTextEditor content={content} onChange={setContent} />
      </View>

      <Flex gap="size-300" wrap alignItems="end">
        <Picker label="Status" selectedKey={status} onSelectionChange={(key) => key && setStatus(String(key))}>
          {STATUS_OPTIONS.map((value) => (
            <Item key={value}>{value}</Item>
          ))}
        </Picker>
        {status === "future" ? (
          <DatePicker
            label="Publish date"
            granularity="minute"
            value={scheduleDate}
            onChange={setScheduleDate}
            placeholderValue={parseAbsoluteToLocal(new Date().toISOString()).set({ hour: 0, minute: 0, second: 0 })}
          />
        ) : null}
        <Switch isSelected={commentsOpen} onChange={setCommentsOpen}>
          Allow comments
        </Switch>
      </Flex>

      {type === "post" ? (
        <Flex gap="size-400" wrap>
          <TermPicker taxonomy="category" label="Categories" selected={categories} onChange={setCategories} />
          <TermPicker taxonomy="post_tag" label="Tags" selected={tags} onChange={setTags} />
        </Flex>
      ) : null}

      {!isNew && id ? (
        <RevisionsPanel postId={id} currentContent={content} onRestored={() => load()} />
      ) : null}

      <ButtonGroup>
        <Button variant="accent" onPress={() => void handleSave()} isDisabled={isSaving}>
          Save
        </Button>
        {!isNew ? (
          <DialogTrigger>
            <Button variant="negative">Delete</Button>
            {(close) => (
              <AlertDialog
                title={`Delete this ${kind}?`}
                variant="destructive"
                primaryActionLabel="Delete"
                cancelLabel="Cancel"
                onPrimaryAction={() => {
                  void handleDelete();
                  close();
                }}
              >
                This cannot be undone.
              </AlertDialog>
            )}
          </DialogTrigger>
        ) : null}
      </ButtonGroup>

      <ConflictDialog
        isOpen={conflict !== null}
        currentModified={conflict?.currentModified ?? ""}
        onReloadLatest={reloadLatest}
        onKeepEditing={() => setConflict(null)}
      />
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
    hour: "numeric",
    minute: "2-digit",
  });
}
