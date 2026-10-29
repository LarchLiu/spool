import { useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import type { KeyboardEvent } from "react";
import { createPortal } from "react-dom";
import { Check, ChevronDown } from "lucide-react";
import { selectPopupLayout } from "./custom-select-layout.js";

export type SelectOption = { value: string; label: string; disabled?: boolean };

export function CustomSelect({
  value,
  options,
  label,
  disabled = false,
  onChange,
}: {
  value: string;
  options: readonly SelectOption[];
  label: string;
  disabled?: boolean;
  onChange: (value: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [layout, setLayout] = useState<ReturnType<typeof selectPopupLayout>>();
  const trigger = useRef<HTMLButtonElement>(null);
  const menu = useRef<HTMLUListElement>(null);
  const initial = useRef("selected");
  const search = useRef({ text: "", time: 0 });
  const id = useId();
  const selected = options.find((option) => option.value === value);
  const blocked = () => disabled || !!trigger.current?.matches(":disabled");
  const focusOption = (option: HTMLElement | undefined) => {
    if (!option || !menu.current) return;
    option.focus({ preventScroll: true });
    const list = menu.current;
    if (option.offsetTop < list.scrollTop) list.scrollTop = option.offsetTop;
    else if (
      option.offsetTop + option.offsetHeight >
      list.scrollTop + list.clientHeight
    )
      list.scrollTop =
        option.offsetTop + option.offsetHeight - list.clientHeight;
  };
  const enabledOptions = () => [
    ...(menu.current?.querySelectorAll<HTMLButtonElement>(
      '[role="option"]:not(:disabled)',
    ) ?? []),
  ];
  const close = (restoreFocus = false) => {
    setOpen(false);
    if (restoreFocus) trigger.current?.focus({ preventScroll: true });
  };

  useLayoutEffect(() => {
    if (!open || !trigger.current || !menu.current) return;
    const button = trigger.current;
    const list = menu.current;
    // A popover escapes clipped settings cards and stays above a modal dialog.
    // Keeping the portal inside that dialog also supports older WebViews.
    list.showPopover?.();
    const update = () => {
      const anchor = button.getBoundingClientRect();
      const visible = window.visualViewport;
      const view = {
        x: visible?.offsetLeft ?? 0,
        y: visible?.offsetTop ?? 0,
        width: visible?.width ?? innerWidth,
        height: visible?.height ?? innerHeight,
      };
      if (
        button.matches(":disabled") ||
        !anchor.width ||
        !anchor.height ||
        anchor.bottom <= view.y ||
        anchor.top >= view.y + view.height
      ) {
        setOpen(false);
        return;
      }
      setLayout(selectPopupLayout(anchor, view, options.length));
    };
    update();
    const available = enabledOptions();
    focusOption(
      initial.current === "last"
        ? available.at(-1)
        : available.find(
            (option) => option.getAttribute("aria-selected") === "true",
          ) || available[0],
    );
    const observer = new ResizeObserver(update);
    observer.observe(button);
    const disabledObserver = new MutationObserver(update);
    for (
      let parent = button.parentElement;
      parent;
      parent = parent.parentElement
    )
      if (parent instanceof HTMLFieldSetElement)
        disabledObserver.observe(parent, {
          attributes: true,
          attributeFilter: ["disabled"],
        });
    window.addEventListener("resize", update);
    window.addEventListener("scroll", update, true);
    window.visualViewport?.addEventListener("resize", update);
    window.visualViewport?.addEventListener("scroll", update);
    return () => {
      observer.disconnect();
      disabledObserver.disconnect();
      window.removeEventListener("resize", update);
      window.removeEventListener("scroll", update, true);
      window.visualViewport?.removeEventListener("resize", update);
      window.visualViewport?.removeEventListener("scroll", update);
      if (list.hidePopover && list.matches(":popover-open")) list.hidePopover();
    };
  }, [open, options.length]);

  useEffect(() => {
    if (disabled) setOpen(false);
  }, [disabled]);
  useEffect(() => {
    if (!open) return;
    const dismiss = (event: PointerEvent) => {
      const target = event.target as Node;
      if (!trigger.current?.contains(target) && !menu.current?.contains(target))
        close();
    };
    document.addEventListener("pointerdown", dismiss, true);
    return () => document.removeEventListener("pointerdown", dismiss, true);
  }, [open]);

  const navigate = (event: KeyboardEvent<HTMLElement>) => {
    if (event.nativeEvent.isComposing) return;
    if (event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      close(true);
      return;
    }
    if (event.key === "Tab") {
      close(true);
      return;
    }
    const available = enabledOptions();
    const index = available.indexOf(
      document.activeElement as HTMLButtonElement,
    );
    let next =
      event.key === "ArrowDown"
        ? (index + 1) % available.length
        : event.key === "ArrowUp"
          ? (index - 1 + available.length) % available.length
          : event.key === "Home"
            ? 0
            : event.key === "End"
              ? available.length - 1
              : -1;
    if (
      event.key.length === 1 &&
      event.key !== " " &&
      !event.ctrlKey &&
      !event.altKey &&
      !event.metaKey
    ) {
      const text = event.key.toLocaleLowerCase();
      search.current = {
        text:
          event.timeStamp - search.current.time > 700
            ? text
            : search.current.text + text,
        time: event.timeStamp,
      };
      const query = [...search.current.text].every((char) => char === text)
        ? text
        : search.current.text;
      for (let offset = 1; offset <= available.length; offset++) {
        const candidate = (index + offset) % available.length;
        if (
          available[candidate].textContent
            ?.trim()
            .toLocaleLowerCase()
            .startsWith(query)
        ) {
          next = candidate;
          break;
        }
      }
    }
    if (next >= 0) {
      event.preventDefault();
      event.stopPropagation();
      focusOption(available[next]);
    }
  };

  return (
    <div className="custom-select">
      <button
        type="button"
        ref={trigger}
        className="custom-select-trigger"
        aria-label={label}
        aria-describedby={`${id}-value`}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={open ? id : undefined}
        disabled={disabled || !options.length}
        onClick={() => {
          initial.current = "selected";
          search.current = { text: "", time: 0 };
          setOpen(!open);
        }}
        onKeyDown={(event) => {
          if (event.key === "ArrowDown" || event.key === "ArrowUp") {
            event.preventDefault();
            event.stopPropagation();
            initial.current =
              !selected && event.key === "ArrowUp" ? "last" : "selected";
            search.current = { text: "", time: 0 };
            if (!blocked()) setOpen(true);
          } else if (open) navigate(event);
        }}
      >
        <span id={`${id}-value`}>{selected?.label ?? value}</span>
        <ChevronDown size={14} aria-hidden="true" />
      </button>
      {open &&
        trigger.current &&
        createPortal(
          <ul
            ref={menu}
            id={id}
            role="listbox"
            aria-label={label}
            className="custom-select-menu"
            popover="manual"
            data-side={layout?.side}
            style={
              layout
                ? {
                    left: layout.left,
                    top: layout.top,
                    width: layout.width,
                    maxHeight: layout.maxHeight,
                  }
                : { visibility: "hidden" }
            }
            onKeyDown={navigate}
            onBlur={(event) => {
              if (
                !event.currentTarget.contains(event.relatedTarget) &&
                event.relatedTarget !== trigger.current
              )
                close();
            }}
          >
            {options.map((option) => (
              <li key={option.value} role="none">
                <button
                  type="button"
                  role="option"
                  tabIndex={-1}
                  data-value={option.value}
                  aria-selected={option.value === value}
                  disabled={option.disabled}
                  onClick={() => {
                    if (blocked() || option.disabled) return;
                    close(true);
                    if (option.value !== value) onChange(option.value);
                  }}
                >
                  <span>{option.label}</span>
                  {option.value === value && (
                    <Check size={14} aria-hidden="true" />
                  )}
                </button>
              </li>
            ))}
          </ul>,
          trigger.current.closest("dialog") || document.body,
        )}
    </div>
  );
}
