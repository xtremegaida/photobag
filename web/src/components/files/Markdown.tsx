import { Typography } from "@mantine/core";
import Markdown from "react-markdown";
import { Link } from "react-router";
import remarkGfm from "remark-gfm";
import { fileRawUrl, filesRoute } from "../../api/files";
import { resolveNoteLink } from "../../lib/files";
import classes from "./Files.module.css";

/**
 * Renders a Markdown note. Raw HTML in the note is shown as text, never run;
 * relative links and images resolve against the note's folder in the bag.
 */
export default function MarkdownNote({ text, notePath }: { text: string; notePath: string }) {
  return (
    <Typography className={classes.markdown}>
      <Markdown
        remarkPlugins={[remarkGfm]}
        components={{
          a: ({ href, children }) => {
            const inBag = resolveNoteLink(notePath, href ?? "");
            if (inBag !== null) return <Link to={filesRoute(inBag)}>{children}</Link>;
            if (href?.startsWith("#")) return <a href={href}>{children}</a>;
            return (
              <a href={href} target="_blank" rel="noreferrer noopener">
                {children}
              </a>
            );
          },
          img: ({ src, alt, title }) => {
            const s = typeof src === "string" ? src : "";
            const inBag = resolveNoteLink(notePath, s);
            return <img src={inBag !== null ? fileRawUrl(inBag) : s} alt={alt ?? ""} title={title} loading="lazy" />;
          },
        }}
      >
        {text}
      </Markdown>
    </Typography>
  );
}
