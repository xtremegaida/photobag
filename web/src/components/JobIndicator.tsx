import { Badge, Group, Loader, Progress, Text, Tooltip, UnstyledButton } from "@mantine/core";
import { useNavigate } from "react-router";
import type { Job } from "../api/types";
import { isActive, useJobStore } from "../stores/jobs";

/** Fraction done (0..1) for known progress shapes, or undefined. */
export function jobFraction(job: Job): number | undefined {
  const p = job.progress as Record<string, unknown> | undefined;
  if (!p) return undefined;
  const done = Number(p.done);
  const total = Number(p.found ?? p.total ?? p.prompts);
  if (Number.isFinite(done) && Number.isFinite(total) && total > 0) return Math.min(1, done / total);
  return undefined;
}

/** Header widget showing the running job, if any. */
export function JobIndicator() {
  const navigate = useNavigate();
  const jobs = useJobStore((s) => s.jobs);
  const active = Object.values(jobs)
    .filter(isActive)
    .sort((a, b) => a.createdAt - b.createdAt);
  if (active.length === 0) return null;
  const job = active.find((j) => j.status === "running") ?? active[0];
  const frac = jobFraction(job);
  return (
    <Tooltip label={job.message || job.title}>
      <UnstyledButton onClick={() => navigate("/jobs")}>
        <Group gap={8} wrap="nowrap">
          <Loader size="xs" />
          <Text size="sm" truncate maw={220} visibleFrom="md">
            {job.title}
          </Text>
          {frac !== undefined && <Progress value={frac * 100} w={80} size="sm" />}
          {active.length > 1 && (
            <Badge size="sm" variant="light">
              +{active.length - 1}
            </Badge>
          )}
        </Group>
      </UnstyledButton>
    </Tooltip>
  );
}
