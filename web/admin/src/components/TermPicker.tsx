import { ActionButton, Checkbox, Flex, Heading, Text, TextField } from "@adobe/react-spectrum";
import { useEffect, useState } from "react";
import type { TermSummary } from "../api/types";
import { api } from "../api/client";
import { useAsync } from "../hooks";
import { ErrorState, Forbidden, Loading } from "./States";

interface TermPickerProps {
  taxonomy: string;
  label: string;
  selected: TermSummary[];
  onChange: (next: TermSummary[]) => void;
}

// slugify mirrors the server's expectation that POST /admin/api/terms
// receives an explicit slug (Req 2.3) — the backend does not derive one from
// name itself, so the client must.
function slugify(name: string): string {
  return name
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/(^-|-$)/g, "");
}

// singularize turns the section heading ("Categories"/"Tags") into the
// inline-create field label ("Category"/"Tag") without requiring callers to
// pass a second, redundant label prop.
function singularize(word: string): string {
  if (word.endsWith("ies")) return `${word.slice(0, -3)}y`;
  if (word.endsWith("s")) return word.slice(0, -1);
  return word;
}

// TermPicker is a taxonomy-scoped multi-select (Req 2) used by PostEditor
// once per taxonomy ("category", "post_tag"). Authors toggle existing terms
// on/off and can create a new term inline without leaving the post editor
// (Req 2.2) — the new term is immediately selected once created.
export function TermPicker({ taxonomy, label, selected, onChange }: TermPickerProps) {
  const asyncState = useAsync((signal) => api.listTerms(taxonomy, signal), [taxonomy]);
  const [available, setAvailable] = useState<TermSummary[]>([]);
  const [newName, setNewName] = useState("");
  const [isCreating, setCreating] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);

  useEffect(() => {
    if (asyncState.status === "success") setAvailable(asyncState.data.items);
  }, [asyncState]);

  if (asyncState.status === "loading") return <Loading label={`Loading ${label.toLowerCase()}`} />;
  if (asyncState.status === "forbidden") return <Forbidden />;
  if (asyncState.status === "error") return <ErrorState message={asyncState.message} />;

  // The picker must keep showing already-selected terms even if they were
  // somehow dropped from a refetched `available` list (e.g. deleted by
  // another session mid-edit), so the union — not just `available` — drives
  // the checkbox list.
  const byId = new Map<number, TermSummary>();
  for (const item of available) byId.set(item.id, item);
  for (const item of selected) byId.set(item.id, item);
  const options = Array.from(byId.values()).sort((a, b) => a.name.localeCompare(b.name));
  const selectedIds = new Set(selected.map((item) => item.id));

  function toggle(term: TermSummary, checked: boolean) {
    if (checked) {
      onChange([...selected, term]);
    } else {
      onChange(selected.filter((item) => item.id !== term.id));
    }
  }

  async function submitNew() {
    const name = newName.trim();
    if (!name || isCreating) return;
    setCreating(true);
    setCreateError(null);
    try {
      const created = await api.createTerm({ name, slug: slugify(name), taxonomy });
      setAvailable((prev) => [...prev, created]);
      onChange([...selected, created]);
      setNewName("");
    } catch (err) {
      setCreateError(err instanceof Error ? err.message : "Could not create term.");
    } finally {
      setCreating(false);
    }
  }

  return (
    <Flex direction="column" gap="size-100">
      <Heading level={4}>{label}</Heading>
      <Flex direction="column" gap="size-75">
        {options.length === 0 ? (
          <Text>None yet.</Text>
        ) : (
          options.map((item) => (
            <Checkbox key={item.id} isSelected={selectedIds.has(item.id)} onChange={(checked) => toggle(item, checked)}>
              {item.name}
            </Checkbox>
          ))
        )}
      </Flex>
      <Flex gap="size-100" alignItems="end" wrap>
        <TextField
          label={`New ${singularize(label)}`}
          value={newName}
          onChange={setNewName}
          onKeyDown={(e) => {
            if (e.key !== "Enter") return;
            e.preventDefault();
            void submitNew();
          }}
        />
        <ActionButton onPress={() => void submitNew()} isDisabled={isCreating || newName.trim() === ""}>
          <Text>Add</Text>
        </ActionButton>
      </Flex>
      {createError ? <Text UNSAFE_style={{ color: "var(--spectrum-global-color-red-600)" }}>{createError}</Text> : null}
    </Flex>
  );
}
