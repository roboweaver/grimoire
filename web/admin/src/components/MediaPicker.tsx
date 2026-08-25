import {
  Content,
  Dialog,
  DialogContainer,
  Divider,
  Grid,
  Heading,
  Image,
  Text,
} from "@adobe/react-spectrum";
import type { MediaItem } from "../api/types";
import { api } from "../api/client";
import { Empty, ErrorState, Forbidden, Loading } from "./States";
import { useAsync } from "../hooks";

interface MediaPickerProps {
  isOpen: boolean;
  onOpenChange: (isOpen: boolean) => void;
  onSelect: (item: MediaItem) => void;
}

// MediaPicker is a new dialog (Req 7.1) that lets an author pick from
// already-uploaded media to insert as an <img> in the rich-text editor. It
// only selects; uploading remains the job of the standalone Media view.
export function MediaPicker({ isOpen, onOpenChange, onSelect }: MediaPickerProps) {
  return (
    <DialogContainer onDismiss={() => onOpenChange(false)}>
      {isOpen ? (
        <Dialog size="L">
          <Heading>Select image</Heading>
          <Divider />
          <Content>
            <MediaPickerBody onSelect={onSelect} onOpenChange={onOpenChange} />
          </Content>
        </Dialog>
      ) : null}
    </DialogContainer>
  );
}

function MediaPickerBody({
  onSelect,
  onOpenChange,
}: {
  onSelect: (item: MediaItem) => void;
  onOpenChange: (isOpen: boolean) => void;
}) {
  const state = useAsync((signal) => api.media({}, signal), []);

  if (state.status === "loading") return <Loading label="Loading media" />;
  if (state.status === "forbidden") return <Forbidden />;
  if (state.status === "error") return <ErrorState message={state.message} />;

  const images = state.data.items.filter((item) => item.mimeType.startsWith("image/"));
  if (images.length === 0) {
    return <Empty heading="No media" message="Upload an image on the Media page first." />;
  }

  function pick(item: MediaItem) {
    onSelect(item);
    onOpenChange(false);
  }

  return (
    <Grid columns="repeat(auto-fill, minmax(size-2000, 1fr))" gap="size-200">
      {images.map((item) => (
        <button
          key={item.id}
          type="button"
          onClick={() => pick(item)}
          style={{
            display: "flex",
            flexDirection: "column",
            gap: "4px",
            padding: "8px",
            border: "1px solid var(--spectrum-global-color-gray-300)",
            borderRadius: "4px",
            background: "none",
            cursor: "pointer",
            textAlign: "left",
          }}
        >
          <Image src={item.url} alt={item.title || item.filename} objectFit="cover" height="size-1600" />
          <Text>{item.title || item.filename}</Text>
        </button>
      ))}
    </Grid>
  );
}
