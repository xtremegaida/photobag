import { Stack, Text, Title } from "@mantine/core";
import { useJobStore } from "../stores/jobs";
import { JobCard } from "./JobCard";

/** The most recent jobs of some kinds, live-updating. */
export function RecentJobs({ kinds, limit = 5, title = "Recent" }: { kinds: string[]; limit?: number; title?: string }) {
  const jobs = useJobStore((s) => s.jobs);
  const list = Object.values(jobs)
    .filter((j) => kinds.includes(j.kind))
    .sort((a, b) => b.createdAt - a.createdAt)
    .slice(0, limit);
  return (
    <Stack gap="sm">
      <Title order={5}>{title}</Title>
      {list.length === 0 ? (
        <Text size="sm" c="dimmed">
          Nothing yet in this session.
        </Text>
      ) : (
        list.map((j) => <JobCard key={j.id} job={j} />)
      )}
    </Stack>
  );
}
