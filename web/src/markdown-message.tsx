import {
  createContext,
  memo,
  useContext,
  useEffect,
  useRef,
  useState,
  type ComponentPropsWithoutRef,
  type ReactNode,
} from "react";
import { Check, CircleAlert, CodeXml, Copy } from "lucide-react";
import ReactMarkdown, {
  defaultUrlTransform,
  type Components,
  type ExtraProps,
} from "react-markdown";
import remarkGfm from "remark-gfm";
import { LocalFileContext } from "./local-file-context.js";
import { localFileTarget } from "./local-file-links.js";
import { copyMessageText } from "./copy-text.js";

type CodeTranslator = (
  key: "Plain text" | "Copy code" | "Code copied" | "Could not copy code",
) => string;
const defaultTranslator: CodeTranslator = (key) => key;
const MarkdownTranslator = createContext(defaultTranslator);

function MarkdownCodeBlock({
  children,
  node,
}: ComponentPropsWithoutRef<"pre"> & ExtraProps) {
  const t = useContext(MarkdownTranslator);
  const code = node?.children.find(
    (child) => child.type === "element" && child.tagName === "code",
  );
  const classes = code?.type === "element" ? code.properties.className : [];
  const languageClass = (Array.isArray(classes) ? classes : []).find((value) =>
    String(value).startsWith("language-"),
  );
  const language = languageClass ? String(languageClass).slice(9) : "";
  const label =
    !language || /^(?:text|txt|plaintext)$/i.test(language)
      ? t("Plain text")
      : language;
  // CommonMark appends one terminal newline, including to a partial streamed fence.
  const text = (
    code?.type === "element"
      ? code.children
          .map((child) => (child.type === "text" ? child.value : ""))
          .join("")
      : ""
  ).replace(/\n$/, "");
  const [feedback, setFeedback] = useState<{
    text: string;
    state: "copied" | "failed";
  } | null>(null);
  const request = useRef(0);
  useEffect(() => {
    setFeedback(null);
    return () => {
      request.current++;
    };
  }, [text]);
  useEffect(() => {
    if (!feedback) return;
    const timeout = setTimeout(() => setFeedback(null), 2000);
    return () => clearTimeout(timeout);
  }, [feedback]);
  const state = feedback?.text === text ? feedback.state : "idle";
  const copyLabel = t(
    state === "copied"
      ? "Code copied"
      : state === "failed"
        ? "Could not copy code"
        : "Copy code",
  );
  const Icon =
    state === "copied" ? Check : state === "failed" ? CircleAlert : Copy;
  return (
    <div className="markdown-code-block">
      <div className="markdown-code-header">
        <CodeXml size={14} aria-hidden="true" />
        <span className="markdown-code-language" title={label}>
          {label}
        </span>
        <button
          type="button"
          className="markdown-code-copy"
          aria-label={copyLabel}
          title={copyLabel}
          disabled={!text}
          onClick={async () => {
            const current = ++request.current;
            try {
              await copyMessageText(text);
              if (request.current === current)
                setFeedback({ text, state: "copied" });
            } catch {
              if (request.current === current)
                setFeedback({ text, state: "failed" });
            }
          }}
        >
          <Icon size={13} aria-hidden="true" />
        </button>
        <span className="message-copy-status" role="status">
          {state !== "idle" ? copyLabel : ""}
        </span>
      </div>
      <pre>{children}</pre>
    </div>
  );
}

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
  pre: MarkdownCodeBlock,
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
  t = defaultTranslator,
}: {
  children: string;
  t?: CodeTranslator;
}) {
  return (
    <div className="markdown">
      <MarkdownTranslator.Provider value={t}>
        <ReactMarkdown
          remarkPlugins={plugins}
          components={components}
          urlTransform={messageURL}
          skipHtml
        >
          {children}
        </ReactMarkdown>
      </MarkdownTranslator.Provider>
    </div>
  );
});
