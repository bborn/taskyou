import DOMPurify from "dompurify";
import { marked } from "marked";
import { useMemo } from "react";

marked.setOptions({ gfm: true, breaks: true });

export function Markdown({ source }: { source: string }) {
  const html = useMemo(() => {
    const raw = marked.parse(source, { async: false }) as string;
    return DOMPurify.sanitize(raw);
  }, [source]);

  return <div className="markdown" dangerouslySetInnerHTML={{ __html: html }} />;
}
