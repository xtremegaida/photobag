import { Stack, Text } from "@mantine/core";
import { JobCard } from "../components/JobCard";
import { Page } from "../components/Page";
import { useJobStore } from "../stores/jobs";

export function JobsPage() {
  const jobs = useJobStore((s) => s.jobs);
  const list = Object.values(jobs).sort((a, b) => b.createdAt - a.createdAt);
  return (
    <Page title="Jobs" description="Background work since PhotoBag started. Jobs run one at a time, in order.">
      <Stack gap="sm" maw={900}>
        {list.length === 0 && <Text c="dimmed">No jobs yet.</Text>}
        {list.map((j) => (
          <JobCard key={j.id} job={j} />
        ))}
      </Stack>
    </Page>
  );
}
