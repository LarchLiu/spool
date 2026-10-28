import { memo, type ReactNode } from "react";
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

function previewBlock({ children }: { children?: ReactNode }) {
  return <span>{children} </span>;
}

function previewHeading({ children }: { children?: ReactNode }) {
  return <strong>{children} </strong>;
}

// Summaries live inside buttons: keep Markdown emphasis without block layout
// or nested interactive elements. Parse the full source before CSS clips it.
const previewComponents: Components = {
  p: previewBlock,
  h1: previewHeading,
  h2: previewHeading,
  h3: previewHeading,
  h4: previewHeading,
  h5: previewHeading,
  h6: previewHeading,
  blockquote: previewBlock,
  ul: previewBlock,
  ol: previewBlock,
  li: previewBlock,
  pre: previewBlock,
  table: previewBlock,
  thead: previewBlock,
  tbody: previewBlock,
  tr: previewBlock,
  th: previewBlock,
  td: previewBlock,
  br: () => <span> </span>,
  hr: () => <span> </span>,
  input: () => null,
  a: ({ children }) => <span>{children}</span>,
  img: ({ alt }) => <span>{alt}</span>,
};

export const MarkdownPreview = memo(function MarkdownPreview({
  children,
}: {
  children: string;
}) {
  return (
    <span className="markdown markdown-preview">
      <ReactMarkdown
        remarkPlugins={plugins}
        components={previewComponents}
        skipHtml
      >
        {children}
      </ReactMarkdown>
    </span>
  );
});

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
