import { useLayoutEffect, useRef, type KeyboardEvent } from "react";
import { turnExchanges } from "./turn-exchanges";
import type { Chat } from "./types";

export function composerHistoryMessages(chat: Chat | null): string[] {
  if (!chat) return [];
  return chat.turns.flatMap((turn) => {
    const parts = chat.transcript.parts.slice(turn.part_start, turn.part_end);
    return turnExchanges(parts).flatMap((exchange) => {
      const text = parts
        .slice(exchange.start, exchange.inputEnd)
        .filter((part) => part.role === "user" && part.kind === "text")
        .map((part) => part.text)
        .join("\n");
      return text.trim() ? [text] : [];
    });
  });
}

type Selection = { value: string; start: number; end: number };
type Navigation = {
  scope: string;
  messages: string[];
  index: number;
  draft: Selection;
  value: string;
};

export function useComposerHistory(
  scope: string,
  chat: Chat | null,
  value: string,
  onChange: (value: string) => void,
) {
  const navigation = useRef<Navigation | null>(null);
  const caret = useRef<(Selection & { element: HTMLTextAreaElement }) | null>(
    null,
  );
  function reset() {
    navigation.current = null;
    caret.current = null;
  }
  useLayoutEffect(() => {
    if (
      navigation.current &&
      (navigation.current.scope !== scope || navigation.current.value !== value)
    ) {
      reset();
    }
    const pending = caret.current;
    if (pending?.value === value) {
      pending.element.setSelectionRange(pending.start, pending.end);
      caret.current = null;
    }
  }, [scope, value]);

  function onKeyDown(event: KeyboardEvent<HTMLTextAreaElement>): boolean {
    if (
      (event.key !== "ArrowUp" && event.key !== "ArrowDown") ||
      event.altKey ||
      event.ctrlKey ||
      event.metaKey ||
      event.shiftKey ||
      event.nativeEvent.isComposing ||
      event.nativeEvent.keyCode === 229
    )
      return false;
    const element = event.currentTarget;
    const { selectionStart: start, selectionEnd: end } = element;
    if (start !== end) return false;
    const up = event.key === "ArrowUp";
    // Let the browser move through multiline and wrapped text. A single
    // rendered line can recall history regardless of its horizontal caret.
    if (up ? start !== 0 : end !== value.length) {
      const style = getComputedStyle(element);
      const oneLine =
        parseFloat(style.lineHeight) +
        parseFloat(style.paddingTop) +
        parseFloat(style.paddingBottom);
      if (element.scrollHeight > Math.ceil(oneLine) + 1) return false;
    }
    let current = navigation.current;
    if (!current || current.scope !== scope || current.value !== value) {
      if (!up) return false;
      const messages = composerHistoryMessages(chat);
      if (!messages.length) return false;
      current = {
        scope,
        messages,
        index: messages.length,
        draft: { value, start, end },
        value,
      };
      navigation.current = current;
    }
    const index = Math.max(
      0,
      Math.min(current.messages.length, current.index + (up ? -1 : 1)),
    );
    const text = current.messages[index];
    const next =
      index === current.messages.length
        ? current.draft
        : {
            value: text,
            start: text.length,
            end: text.length,
          };
    event.preventDefault();
    current.index = index;
    current.value = next.value;
    caret.current = { ...next, element };
    if (next.value === value) {
      element.setSelectionRange(next.start, next.end);
      caret.current = null;
    }
    onChange(next.value);
    if (index === current.messages.length) navigation.current = null;
    return true;
  }
  return { onKeyDown, reset };
}
