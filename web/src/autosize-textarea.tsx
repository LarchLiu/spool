import { useLayoutEffect, useRef, type ComponentPropsWithoutRef } from "react";

type Props = Omit<ComponentPropsWithoutRef<"textarea">, "value"> & {
  value: string;
  maxRows?: number;
};

export function resizeTextarea(textarea: HTMLTextAreaElement, maxRows = 6) {
  const style = getComputedStyle(textarea);
  const padding =
    parseFloat(style.paddingTop) + parseFloat(style.paddingBottom);
  const border =
    parseFloat(style.borderTopWidth) + parseFloat(style.borderBottomWidth);
  const lineHeight = parseFloat(style.lineHeight);
  const minimum = Math.ceil(lineHeight + padding + border);
  const maximum = Math.ceil(
    Math.max(1, maxRows) * lineHeight + padding + border,
  );
  // Collapsing the live input temporarily expands the history viewport and
  // clamps its scroll position. Measure outside the layout instead.
  const measure = textarea.cloneNode(false) as HTMLTextAreaElement;
  measure.removeAttribute("id");
  measure.removeAttribute("name");
  measure.removeAttribute("autofocus");
  measure.setAttribute("aria-hidden", "true");
  measure.tabIndex = -1;
  measure.value = textarea.value;
  Object.assign(measure.style, {
    position: "absolute",
    visibility: "hidden",
    pointerEvents: "none",
    top: "0",
    left: "0",
    boxSizing: "border-box",
    width: `${textarea.getBoundingClientRect().width}px`,
    height: "0px",
    minHeight: "0px",
    maxHeight: "none",
    overflow: "hidden",
  });
  textarea.after(measure);
  let content: number;
  try {
    content = measure.scrollHeight + border;
  } finally {
    measure.remove();
  }
  textarea.style.height = `${Math.max(minimum, Math.min(content, maximum))}px`;
  textarea.style.overflowY = content > maximum ? "auto" : "hidden";
}

export function AutosizeTextarea({ value, maxRows = 6, ...props }: Props) {
  const ref = useRef<HTMLTextAreaElement>(null);
  useLayoutEffect(() => {
    if (ref.current) resizeTextarea(ref.current, maxRows);
  }, [value, maxRows]);
  useLayoutEffect(() => {
    const textarea = ref.current!;
    let width = textarea.getBoundingClientRect().width;
    const observer = new ResizeObserver(() => {
      const nextWidth = textarea.getBoundingClientRect().width;
      // Height changes must not trigger another sizing pass.
      if (nextWidth === width) return;
      width = nextWidth;
      resizeTextarea(textarea, maxRows);
    });
    observer.observe(textarea);
    return () => observer.disconnect();
  }, [maxRows]);
  return <textarea {...props} rows={1} ref={ref} value={value} />;
}
