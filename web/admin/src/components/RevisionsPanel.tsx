import { ActionButton, Flex, Heading, Text } from "@adobe/react-spectrum";
import { useState } from "react";
import { api } from "../api/client";
import type { RevisionDetail, RevisionSummary } from "../api/types";
import { useAsync } from "../hooks";
import { ErrorState, Forbidden, Loading } from "./States";

interface RevisionsPanelProps {
  postId: number | string;
  currentContent: string;
  onRestored: () => void;
}

interface DiffSegment {
  type: "same" | "added" | "removed";
  value: string;
}

const MAX_LCS_MATRIX_CELLS = 100_000;

// diffWords computes a minimal word-level diff between two content strings
// entirely on the client, so selecting a revision never issues an extra
// server round trip for the comparison (Req 8.2). It tokenizes on whitespace
// boundaries (keeping the whitespace itself as tokens) and finds an LCS-based
// alignment. Large comparisons fall back to a whole-text replacement before
// allocating the quadratic LCS matrix.
function diffWords(oldText: string, newText: string): DiffSegment[] {
  const a = oldText.split(/(\s+)/).filter((token) => token.length > 0);
  const b = newText.split(/(\s+)/).filter((token) => token.length > 0);
  const m = a.length;
  const n = b.length;

  if ((m + 1) * (n + 1) > MAX_LCS_MATRIX_CELLS) {
    if (oldText === newText) {
      return oldText ? [{ type: "same", value: oldText }] : [];
    }
    const segments: DiffSegment[] = [];
    if (oldText) segments.push({ type: "removed", value: oldText });
    if (newText) segments.push({ type: "added", value: newText });
    return segments;
  }

  const lcs: number[][] = Array.from({ length: m + 1 }, () =>
    new Array<number>(n + 1).fill(0),
  );
  for (let i = m - 1; i >= 0; i--) {
    for (let j = n - 1; j >= 0; j--) {
      lcs[i][j] =
        a[i] === b[j]
          ? lcs[i + 1][j + 1] + 1
          : Math.max(lcs[i + 1][j], lcs[i][j + 1]);
    }
  }

  const segments: DiffSegment[] = [];
  let i = 0;
  let j = 0;
  while (i < m && j < n) {
    if (a[i] === b[j]) {
      segments.push({ type: "same", value: a[i] });
      i++;
      j++;
    } else if (lcs[i + 1][j] >= lcs[i][j + 1]) {
      segments.push({ type: "removed", value: a[i] });
      i++;
    } else {
      segments.push({ type: "added", value: b[j] });
      j++;
    }
  }
  while (i < m) {
    segments.push({ type: "removed", value: a[i] });
    i++;
  }
  while (j < n) {
    segments.push({ type: "added", value: b[j] });
    j++;
  }
  return segments;
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

// RevisionsPanel lists a post's revisions, renders a client-side diff of a
// selected revision against the live editor content, and offers a restore
// action that reloads the editor with the restored post (Req 8.1-8.3).
export function RevisionsPanel({
  postId,
  currentContent,
  onRestored,
}: RevisionsPanelProps) {
  const listState = useAsync(
    (signal) => api.listRevisions(postId, signal),
    [postId],
  );
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const [selected, setSelected] = useState<RevisionDetail | null>(null);
  const [isRestoring, setIsRestoring] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function selectRevision(rev: RevisionSummary) {
    setSelectedId(rev.id);
    setSelected(null);
    setError(null);
    try {
      const detail = await api.getRevision(postId, rev.id);
      setSelected(detail);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Something went wrong.");
    }
  }

  async function restoreSelected() {
    if (selectedId == null) return;
    setIsRestoring(true);
    setError(null);
    try {
      await api.restoreRevision(postId, selectedId);
      onRestored();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Something went wrong.");
    } finally {
      setIsRestoring(false);
    }
  }

  if (listState.status === "loading") return <Loading label="Loading revisions" />;
  if (listState.status === "forbidden") return <Forbidden />;
  if (listState.status === "error") return <ErrorState message={listState.message} />;

  const revisions = listState.data;

  return (
    <Flex direction="column" gap="size-150">
      <Heading level={4}>Revisions</Heading>
      {revisions.length === 0 ? (
        <Text>No revisions yet.</Text>
      ) : (
        <Flex direction="column" gap="size-75">
          {revisions.map((rev) => (
            <ActionButton
              key={rev.id}
              isQuiet={rev.id !== selectedId}
              onPress={() => void selectRevision(rev)}
            >
              <Text>{`Author ${rev.author} — ${formatDate(rev.modified)}`}</Text>
            </ActionButton>
          ))}
        </Flex>
      )}
      {selected ? (
        <Flex direction="column" gap="size-100">
          <div data-testid="revision-diff">
            {diffWords(selected.content, currentContent).map((seg, idx) => {
              if (seg.type === "removed") {
                return (
                  <del key={idx} data-testid="diff-removed">
                    {seg.value}
                  </del>
                );
              }
              if (seg.type === "added") {
                return (
                  <ins key={idx} data-testid="diff-added">
                    {seg.value}
                  </ins>
                );
              }
              return <span key={idx}>{seg.value}</span>;
            })}
          </div>
          <ActionButton onPress={() => void restoreSelected()} isDisabled={isRestoring}>
            <Text>Restore this revision</Text>
          </ActionButton>
        </Flex>
      ) : null}
      {error ? <Text>{error}</Text> : null}
    </Flex>
  );
}
