import { useLayoutEffect, useRef, type ComponentPropsWithoutRef } from "react";

type Props = Omit<ComponentPropsWithoutRef<"textarea">, "value"> & {
  value: string;
};

function resizeTextarea(textarea: HTMLTextAreaElement) {
  textarea.style.height = "0px";
  textarea.style.overflowY = "hidden";
  const style = getComputedStyle(textarea);
  const padding =
    parseFloat(style.paddingTop) + parseFloat(style.paddingBottom);
  const border =
    parseFloat(style.borderTopWidth) + parseFloat(style.borderBottomWidth);
  const lineHeight = parseFloat(style.lineHeight);
  const minimum = Math.ceil(lineHeight + padding + border);
  const maximum = Math.ceil(6 * lineHeight + padding + border);
  const content = textarea.scrollHeight + border;
  textarea.style.height = `${Math.max(minimum, Math.min(content, maximum))}px`;
  textarea.style.overflowY = content > maximum ? "auto" : "hidden";
}

export function AutosizeTextarea({ value, ...props }: Props) {
  const ref = useRef<HTMLTextAreaElement>(null);
  useLayoutEffect(() => {
    if (ref.current) resizeTextarea(ref.current);
  }, [value]);
  useLayoutEffect(() => {
    const textarea = ref.current!;
    let width = textarea.getBoundingClientRect().width;
    const observer = new ResizeObserver(() => {
      const nextWidth = textarea.getBoundingClientRect().width;
      // Height changes must not trigger another sizing pass.
      if (nextWidth === width) return;
      width = nextWidth;
      resizeTextarea(textarea);
    });
    observer.observe(textarea);
    return () => observer.disconnect();
  }, []);
  return <textarea {...props} rows={1} ref={ref} value={value} />;
}
