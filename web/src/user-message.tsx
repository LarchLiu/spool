import { useId, useLayoutEffect, useRef, useState } from "react";
import { ChevronDown, ChevronUp } from "lucide-react";
import type { Translator } from "./i18n";
import { MarkdownMessage } from "./markdown-message.js";

export function observeUserMessageOverflow(
  content: HTMLDivElement,
  onChange: (overflow: boolean) => void,
) {
  const measure = () => {
    const lineHeight = parseFloat(getComputedStyle(content).lineHeight);
    onChange(content.scrollHeight > lineHeight * 8 + 1);
  };
  measure();
  const observer = new ResizeObserver(measure);
  observer.observe(content);
  // The clipped viewport stays the same height when its Markdown grows.
  if (content.firstElementChild) observer.observe(content.firstElementChild);
  return () => observer.disconnect();
}

export function UserMessage({ text, t }: { text: string; t: Translator }) {
  const id = useId();
  const content = useRef<HTMLDivElement>(null);
  const [overflow, setOverflow] = useState(false);
  const [expanded, setExpanded] = useState(false);

  useLayoutEffect(
    () => observeUserMessageOverflow(content.current!, setOverflow),
    [text],
  );

  return (
    <div className="user-bubble">
      <div
        className="user-bubble-content"
        id={id}
        ref={content}
        data-expanded={expanded}
      >
        <MarkdownMessage>{text}</MarkdownMessage>
      </div>
      {overflow && (
        <button
          type="button"
          className="user-message-toggle"
          aria-controls={id}
          aria-expanded={expanded}
          onClick={() => setExpanded((value) => !value)}
        >
          {t(expanded ? "Show less" : "Show more")}
          {expanded ? <ChevronUp size={14} /> : <ChevronDown size={14} />}
        </button>
      )}
    </div>
  );
}
