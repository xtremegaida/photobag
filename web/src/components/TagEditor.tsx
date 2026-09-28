import { TagsInput, type TagsInputProps } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { useMemo } from "react";
import { errorMessage } from "../api/client";
import { useBulkTags, useTags } from "../api/hooks";

/** Tag names known to the bag, most used first. */
export function useTagNames(): string[] {
  const { data } = useTags();
  return useMemo(() => [...(data ?? [])].sort((a, b) => b.count - a.count).map((t) => t.name), [data]);
}

interface TagEditorProps extends Omit<TagsInputProps, "value" | "onChange" | "data"> {
  imageIds: number[];
  /** Current tags (of a single image). */
  value: string[];
}

/** Edits the tags of images in place: choose existing tags or create new ones. */
export function TagEditor({ imageIds, value, ...rest }: TagEditorProps) {
  const names = useTagNames();
  const bulk = useBulkTags();
  const onChange = (next: string[]) => {
    const lower = (xs: string[]) => new Set(xs.map((x) => x.toLocaleLowerCase()));
    const cur = lower(value);
    const nxt = lower(next);
    const add = next.filter((t) => !cur.has(t.toLocaleLowerCase()));
    const remove = value.filter((t) => !nxt.has(t.toLocaleLowerCase()));
    if (add.length === 0 && remove.length === 0) return;
    bulk.mutate(
      { ids: imageIds, add, remove },
      { onError: (e) => notifications.show({ color: "red", title: "Could not update tags", message: errorMessage(e) }) },
    );
  };
  return (
    <TagsInput
      data={names}
      value={value}
      onChange={onChange}
      placeholder={value.length ? "" : "Add tags…"}
      clearable={false}
      acceptValueOnBlur
      splitChars={[","]}
      limit={30}
      {...rest}
    />
  );
}
