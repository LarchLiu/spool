import { memo, useContext, type ReactNode } from "react";
import ReactMarkdown, {
  defaultUrlTransform,
  type Components,
} from "react-markdown";
import remarkGfm from "remark-gfm";
import { LocalFileContext } from "./local-file-context.js";
import { localFileTarget } from "./local-file-links.js";

function MarkdownLink({
  href,
  title,
  children,
}: {
  href?: string;
  title?: string;
  children?: ReactNode;
}) {
  const { openFile, basePath } = useContext(LocalFileContext);
  const file = href ? localFileTarget(href, basePath) : null;
  if (file)
    return (
      <button
        type="button"
        className="local-file-link"
        title={href}
        disabled={!openFile}
        onClick={() => openFile?.(file)}
      >
        {children}
      </button>
    );
  return href ? (
    <a
      href={href}
      title={title}
      target={href.startsWith("#") ? undefined : "_blank"}
      rel="noopener noreferrer"
    >
      {children}
    </a>
  ) : (
    <span>{children}</span>
  );
}

function messageURL(url: string) {
  if (localFileTarget(url)) return url;
  try {
    return defaultUrlTransform(decodeURIComponent(url))
      ? defaultUrlTransform(url)
      : "";
  } catch {
    return "";
  }
}

const plugins = [remarkGfm];
const components: Components = {
  a: MarkdownLink,
  // Agent output must not silently load remote images or tracking pixels.
  img: ({ src, alt }) =>
    src ? (
      <MarkdownLink href={src}>{alt || src}</MarkdownLink>
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
      <ReactMarkdown
        remarkPlugins={plugins}
        components={components}
        urlTransform={messageURL}
        skipHtml
      >
        {children}
      </ReactMarkdown>
    </div>
  );
});
