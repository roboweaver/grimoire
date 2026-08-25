import { ActionButton, Flex, Item, Picker, Text, ToggleButton } from "@adobe/react-spectrum";
import Image from "@tiptap/extension-image";
import { EditorContent, useEditor, useEditorState } from "@tiptap/react";
import StarterKit from "@tiptap/starter-kit";
import { useEffect, useState } from "react";
import type { MediaItem } from "../api/types";
import { MediaPicker } from "./MediaPicker";
import "./RichTextEditor.css";

interface RichTextEditorProps {
  content: string;
  onChange: (html: string) => void;
}

const HEADING_LEVELS = [2, 3, 4] as const;

// RichTextEditor is the TipTap-based WYSIWYG editor shared by the Posts and
// Pages editor views (Req 7). It supports bold, italic, headings H2-H4,
// bullet/numbered lists, blockquote, link, and image (via MediaPicker) —
// the minimum set required by Req 7.1.
export function RichTextEditor({ content, onChange }: RichTextEditorProps) {
  const [isPickerOpen, setPickerOpen] = useState(false);

  const editor = useEditor({
    extensions: [
      // StarterKit v3 already bundles Link (and Underline); configuring it
      // here avoids registering a second, duplicate Link extension.
      StarterKit.configure({ link: { openOnClick: false } }),
      Image,
    ],
    content,
    onUpdate: ({ editor }) => onChange(editor.getHTML()),
  });

  // TipTap v3's `editor` instance from useEditor is intentionally not
  // reactive on its own (to avoid re-rendering the whole tree on every
  // keystroke) — reading editor.isActive() straight from render only
  // reflects state as of the last unrelated re-render. useEditorState's
  // selector subscribes this component to exactly the toolbar-relevant
  // slice (Req 7.2: toolbar SHALL reflect active mark/node state).
  const toolbarState = useEditorState({
    editor,
    selector: ({ editor }) => {
      if (!editor) return null;
      return {
        bold: editor.isActive("bold"),
        italic: editor.isActive("italic"),
        link: editor.isActive("link"),
        bulletList: editor.isActive("bulletList"),
        orderedList: editor.isActive("orderedList"),
        blockquote: editor.isActive("blockquote"),
        heading: HEADING_LEVELS.find((level) => editor.isActive("heading", { level })) ?? null,
      };
    },
  });

  // Req 7.4: loading a different post's content into an editor that is
  // already mounted (e.g. navigating between edit routes without a full
  // remount) must call setContent rather than silently keep stale content.
  useEffect(() => {
    if (!editor) return;
    if (editor.getHTML() === content) return;
    editor.commands.setContent(content);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [editor, content]);

  if (!editor || !toolbarState) return null;

  const headingKey = toolbarState.heading ? `h${toolbarState.heading}` : "paragraph";

  function setHeading(key: React.Key | null) {
    if (key === null) return;
    if (key === "paragraph") {
      editor!.chain().focus().setParagraph().run();
      return;
    }
    const level = Number(String(key).slice(1)) as 2 | 3 | 4;
    editor!.chain().focus().toggleHeading({ level }).run();
  }

  function toggleLink() {
    if (toolbarState!.link) {
      editor!.chain().focus().unsetLink().run();
      return;
    }
    const url = window.prompt("Link URL");
    if (!url) return;
    editor!.chain().focus().setLink({ href: url }).run();
  }

  function insertImage(item: MediaItem) {
    editor!.chain().focus().setImage({ src: item.url, alt: item.title || item.filename }).run();
  }

  return (
    <Flex direction="column" gap="size-100">
      <Flex gap="size-100" wrap alignItems="center" aria-label="Formatting">
        <Picker aria-label="Text style" selectedKey={headingKey} onSelectionChange={setHeading} width="size-1600">
          <Item key="paragraph">Paragraph</Item>
          <Item key="h2">Heading 2</Item>
          <Item key="h3">Heading 3</Item>
          <Item key="h4">Heading 4</Item>
        </Picker>
        <ActionButton
          aria-label="Bold"
          aria-pressed={toolbarState.bold}
          onPress={() => editor.chain().focus().toggleBold().run()}
        >
          <Text>Bold</Text>
        </ActionButton>
        <ActionButton
          aria-label="Italic"
          aria-pressed={toolbarState.italic}
          onPress={() => editor.chain().focus().toggleItalic().run()}
        >
          <Text>Italic</Text>
        </ActionButton>
        <ActionButton aria-label="Link" aria-pressed={toolbarState.link} onPress={toggleLink}>
          <Text>Link</Text>
        </ActionButton>
        <ToggleButton
          aria-label="Bullet list"
          isSelected={toolbarState.bulletList}
          onChange={() => editor.chain().focus().toggleBulletList().run()}
        >
          <Text>Bullet list</Text>
        </ToggleButton>
        <ToggleButton
          aria-label="Numbered list"
          isSelected={toolbarState.orderedList}
          onChange={() => editor.chain().focus().toggleOrderedList().run()}
        >
          <Text>Numbered list</Text>
        </ToggleButton>
        <ActionButton
          aria-label="Blockquote"
          aria-pressed={toolbarState.blockquote}
          onPress={() => editor.chain().focus().toggleBlockquote().run()}
        >
          <Text>Quote</Text>
        </ActionButton>
        <ActionButton aria-label="Insert image" onPress={() => setPickerOpen(true)}>
          <Text>Image</Text>
        </ActionButton>
      </Flex>
      <div data-testid="richtext-surface" className="richtext-surface">
        <EditorContent editor={editor} />
      </div>
      <MediaPicker isOpen={isPickerOpen} onOpenChange={setPickerOpen} onSelect={insertImage} />
    </Flex>
  );
}

