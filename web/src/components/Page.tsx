import { Group, ScrollArea, SimpleGrid, Stack, Text, Title } from "@mantine/core";
import type { ReactNode } from "react";

/** Scrollable page with a title and a two-column body (form | side panel). */
export function Page({
  title,
  description,
  actions,
  side,
  children,
}: {
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  side?: ReactNode;
  children: ReactNode;
}) {
  return (
    <ScrollArea style={{ flex: 1 }}>
      <Stack p="md" gap="md" maw={1400}>
        <Group justify="space-between" align="flex-start">
          <div>
            <Title order={3}>{title}</Title>
            {description && (
              <Text c="dimmed" size="sm" mt={4} maw={720}>
                {description}
              </Text>
            )}
          </div>
          {actions}
        </Group>
        {side ? (
          <SimpleGrid cols={{ base: 1, lg: 2 }} spacing="xl">
            <Stack gap="md">{children}</Stack>
            <Stack gap="md">{side}</Stack>
          </SimpleGrid>
        ) : (
          children
        )}
      </Stack>
    </ScrollArea>
  );
}
