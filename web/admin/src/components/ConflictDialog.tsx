import { Button, ButtonGroup, Content, Dialog, DialogContainer, Divider, Heading, Text } from "@adobe/react-spectrum";

interface ConflictDialogProps {
  isOpen: boolean;
  currentModified: string;
  onReloadLatest: () => void;
  onKeepEditing: () => void;
}

// ConflictDialog is shown when a post/page save request returns 409
// Conflict (Req 3.2, 9.3): another editor changed the item since this one
// was loaded. It never silently retries the save — the author must
// explicitly choose to discard their edits and reload, or keep editing (and
// reconcile manually before saving again).
export function ConflictDialog({ isOpen, currentModified, onReloadLatest, onKeepEditing }: ConflictDialogProps) {
  return (
    <DialogContainer onDismiss={onKeepEditing}>
      {isOpen ? (
        <Dialog>
          <Heading>This item changed since you loaded it</Heading>
          <Divider />
          <Content>
            <Text>
              Someone else saved a newer version at {currentModified}. Reload the latest version (discarding your
              unsaved changes), or keep editing and reconcile manually before saving again.
            </Text>
          </Content>
          <ButtonGroup>
            <Button variant="secondary" onPress={onKeepEditing}>
              Keep editing
            </Button>
            <Button variant="accent" onPress={onReloadLatest}>
              Reload latest
            </Button>
          </ButtonGroup>
        </Dialog>
      ) : null}
    </DialogContainer>
  );
}
