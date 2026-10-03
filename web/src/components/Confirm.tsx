import { Button, Group, Modal, Stack } from "@mantine/core";
import type { ReactNode } from "react";

/** A confirmation dialog for an action that cannot be undone. */
export function Confirm({
  opened,
  title,
  children,
  confirm,
  cancel = "Cancel",
  color = "red",
  loading,
  onConfirm,
  onClose,
}: {
  opened: boolean;
  title: ReactNode;
  children?: ReactNode;
  confirm: string;
  cancel?: string;
  color?: string;
  loading?: boolean;
  onConfirm: () => void;
  onClose: () => void;
}) {
  return (
    <Modal opened={opened} onClose={onClose} title={title}>
      <Stack gap="md">
        {children}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {cancel}
          </Button>
          <Button color={color} onClick={onConfirm} loading={loading} data-autofocus>
            {confirm}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}
