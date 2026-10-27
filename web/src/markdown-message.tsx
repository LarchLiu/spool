import { memo } from "react";
import ReactMarkdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";

const plugins = [remarkGfm];
const components: Components = {
  a: ({ href, title, children }) =>
    href ? (
      <a href={href} title={title} target="_blank" rel="noopener noreferrer">
        {children}
      </a>
    ) : (
      <span>{children}</span>
    ),
  // Agent output must not silently load remote images or tracking pixels.
  img: ({ src, alt }) =>
    src ? (
      <a href={src} target="_blank" rel="noopener noreferrer">
        {alt || src}
      </a>
    ) : (
      <span>{alt}</span>
    ),
  table: ({ children }) => (
    <div className="markdown-table">
      <table>{children}</table>
    </div>
  ),
};

export const MarkdownMessage = memo(function MarkdownMessage({
  children,
}: {
  children: string;
}) {
  return (
    <div className="markdown">
      <ReactMarkdown remarkPlugins={plugins} components={components} skipHtml>
        {children}
      </ReactMarkdown>
    </div>
  );
});
