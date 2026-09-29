import { Badge, Group, Tooltip } from "@mantine/core";
import { Link } from "react-router";

const ratingColor: Record<string, string> = { general: "teal", sensitive: "yellow", questionable: "orange", explicit: "red" };

interface Props {
  tags?: string[];
  /** Which of tags are character tags (from a tagger). */
  characters?: string[];
  rating?: string;
  ratingScore?: number;
  /** The tagger's confidence per tag, 0–1. */
  scores?: Record<string, number>;
  /** Link each badge to a library search. */
  link?: boolean;
}

const percent = (v: number | undefined) => (v === undefined ? "" : `${Math.round(v * 100)}% confident`);

/** A badge, linking to a search for search when given. */
function badge(label: string, color: string, search: string | undefined) {
  return search === undefined ? (
    <Badge variant="light" color={color} tt="none">
      {label}
    </Badge>
  ) : (
    <Badge component={Link} to={`/?text=${encodeURIComponent(`"${search}"`)}`} variant="light" color={color} tt="none" style={{ cursor: "pointer" }}>
      {label}
    </Badge>
  );
}

/** Danbooru tags as badges: character tags stand out, the rating is coloured. */
export function DanbooruTags({ tags = [], characters = [], rating, ratingScore, scores, link = false }: Props) {
  const chars = new Set(characters);
  return (
    <Group gap={4}>
      {tags.map((t) => (
        <Tooltip key={t} label={chars.has(t) ? `Character · ${percent(scores?.[t])}` : percent(scores?.[t])} disabled={!scores && !chars.has(t)}>
          {badge(t, chars.has(t) ? "grape" : "gray", link ? t : undefined)}
        </Tooltip>
      ))}
      {rating && (
        <Tooltip label={`Rating · ${percent(ratingScore)}`}>
          {badge(`rating: ${rating}`, ratingColor[rating] ?? "gray", link ? `rating:${rating}` : undefined)}
        </Tooltip>
      )}
    </Group>
  );
}
