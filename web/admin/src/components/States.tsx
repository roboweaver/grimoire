import { Flex, IllustratedMessage, Heading, Content } from "@adobe/react-spectrum";
import Document from "@spectrum-icons/workflow/Document";
import Alert from "@spectrum-icons/workflow/Alert";
import LockClosed from "@spectrum-icons/workflow/LockClosed";
import { ProgressCircle } from "@adobe/react-spectrum";

// Loading, Empty, ErrorState and Forbidden are the shared data-state chrome used
// by every data-driven view so no view renders a blank screen (Req 8.3, 8.4).
// They compose Spectrum primitives only — no hardcoded color/spacing.

export function Loading({ label = "Loading" }: { label?: string }) {
  return (
    <Flex alignItems="center" justifyContent="center" height="size-2400">
      <ProgressCircle aria-label={label} isIndeterminate />
    </Flex>
  );
}

export function Empty({
  heading = "Nothing here yet",
  message,
}: {
  heading?: string;
  message?: string;
}) {
  return (
    <IllustratedMessage>
      <Document />
      <Heading>{heading}</Heading>
      {message ? <Content>{message}</Content> : null}
    </IllustratedMessage>
  );
}

export function ErrorState({ message }: { message: string }) {
  return (
    <IllustratedMessage>
      <Alert aria-label="Error" />
      <Heading>Something went wrong</Heading>
      <Content>{message}</Content>
    </IllustratedMessage>
  );
}

export function Forbidden() {
  return (
    <IllustratedMessage>
      <LockClosed aria-label="Insufficient permissions" />
      <Heading>Insufficient permissions</Heading>
      <Content>
        Your account doesn&rsquo;t have access to this area. Ask an administrator
        for the “edit posts” capability.
      </Content>
    </IllustratedMessage>
  );
}
