import { useEffect, useLayoutEffect, useRef, type ReactNode } from "react";

export type SegmentedTab<T extends string> = {
  value: T;
  label: string;
  icon?: ReactNode;
  title?: string;
  accessibleLabel?: string;
  id?: string;
  controls?: string;
  disabled?: boolean;
};

export type TabTransition<T extends string> = {
  from: T;
  startedAt: number;
  scrollLeft?: number;
};

// A scrollable segmented control, also usable for navigation between pages.
// The trailing action belongs to the scroll content, after the last option.
export function SegmentedTabs<T extends string>({
  label,
  value,
  options,
  onChange,
  navigation = false,
  className = "",
  trackClassName = "",
  trailing,
  transition,
}: {
  label: string;
  value: T;
  options: SegmentedTab<T>[];
  onChange: (value: T, scrollLeft: number) => void;
  navigation?: boolean;
  className?: string;
  trackClassName?: string;
  trailing?: ReactNode;
  // Native pages can continue the same animation after switching WebViews.
  transition?: TabTransition<T>;
}) {
  const root = useRef<HTMLElement>(null);
  const viewport = useRef<HTMLDivElement>(null);
  const content = useRef<HTMLDivElement>(null);
  const trailingAction = useRef<HTMLDivElement>(null);
  const indicator = useRef<HTMLSpanElement>(null);
  const buttons = useRef(new Map<T, HTMLButtonElement>());
  const animation = useRef<Animation | null>(null);
  const scrollFrame = useRef<number | undefined>(undefined);
  const position = useRef<IndicatorPosition | null>(null);
  const lastValue = useRef<T | undefined>(undefined);
  const lastTransition = useRef<number | undefined>(undefined);
  const lastScrollTransition = useRef<number | undefined>(undefined);
  const drag = useRef<{
    pointer: number;
    x: number;
    y: number;
    scroll: number;
    active: boolean;
  } | null>(null);
  const suppressClick = useRef(false);
  const Container = navigation ? "nav" : "div";
  const lastOption = options.at(-1)?.value;
  const hasTrailing = !!trailing;
  const stopScroll = () => {
    if (scrollFrame.current !== undefined)
      cancelAnimationFrame(scrollFrame.current);
    scrollFrame.current = undefined;
  };
  const reveal = (target: T, shared?: TabTransition<T>) => {
    const scroller = viewport.current;
    const button = buttons.current.get(target);
    if (!scroller || !button) return;
    stopScroll();
    const start = shared?.scrollLeft ?? scroller.scrollLeft;
    const end =
      target === lastOption && hasTrailing
        ? trailingAction.current || button
        : button;
    const left = button.offsetLeft;
    const right = end.offsetLeft + end.offsetWidth;
    const destination = Math.max(
      0,
      Math.min(
        scroller.scrollWidth - scroller.clientWidth,
        right > start + scroller.clientWidth
          ? right - scroller.clientWidth
          : left < start
            ? left
            : start,
      ),
    );
    const startedAt = shared?.startedAt ?? Date.now();
    const tick = () => {
      const progress = matchMedia("(prefers-reduced-motion: reduce)").matches
        ? 1
        : Math.min(1, Math.max(0, (Date.now() - startedAt) / 220));
      scroller.scrollLeft =
        start + (destination - start) * (1 - (1 - progress) ** 3);
      scrollFrame.current =
        progress < 1 ? requestAnimationFrame(tick) : undefined;
    };
    tick();
  };

  useLayoutEffect(() => {
    const scroller = viewport.current;
    const track = content.current;
    const marker = indicator.current;
    if (!scroller || !track || !marker) return;
    const measure = () => {
      const selected = buttons.current.get(value);
      if (!selected) return;
      root.current?.toggleAttribute(
        "data-overflow",
        scroller.scrollWidth > scroller.clientWidth + 1,
      );
      const next = indicatorPosition(selected);
      const changed = lastValue.current !== value;
      const elapsed = transition ? Date.now() - transition.startedAt : Infinity;
      const replay =
        !!transition &&
        transition.startedAt !== lastTransition.current &&
        elapsed >= 0 &&
        elapsed < 220;
      const previousButton = replay
        ? buttons.current.get(transition.from)
        : undefined;
      const previous = position.current;
      lastValue.current = value;
      lastTransition.current = transition?.startedAt;
      if (previous && samePosition(previous, next) && !replay) return;
      const current = getComputedStyle(marker);
      const from = previousButton
        ? indicatorStyle(indicatorPosition(previousButton))
        : {
            transform: current.transform,
            width: current.width,
            height: current.height,
          };
      animation.current?.cancel();
      const target = indicatorStyle(next);
      Object.assign(marker.style, target);
      root.current?.setAttribute("data-positioned", "");
      position.current = next;
      if (
        ((previous && changed) || previousButton) &&
        !matchMedia("(prefers-reduced-motion: reduce)").matches
      ) {
        animation.current = marker.animate([from, target], {
          duration: 220,
          easing: "cubic-bezier(0.2, 0.8, 0.2, 1)",
        });
        if (previousButton) animation.current.currentTime = elapsed;
      }
    };
    measure();
    const resize = new ResizeObserver(measure);
    resize.observe(scroller);
    resize.observe(track);
    for (const button of buttons.current.values()) resize.observe(button);
    return () => resize.disconnect();
  });

  useLayoutEffect(() => {
    const shared =
      transition?.startedAt !== lastScrollTransition.current
        ? transition
        : undefined;
    lastScrollTransition.current = transition?.startedAt;
    reveal(value, shared);
    return stopScroll;
  }, [value, transition?.startedAt, lastOption, hasTrailing]);

  useEffect(
    () => () => {
      animation.current?.cancel();
      stopScroll();
    },
    [],
  );
  const stopDrag = (pointer: number) => {
    const current = drag.current;
    if (!current || current.pointer !== pointer) return;
    suppressClick.current = current.active;
    drag.current = null;
    root.current?.removeAttribute("data-dragging");
    if (viewport.current?.hasPointerCapture(pointer))
      viewport.current.releasePointerCapture(pointer);
  };

  return (
    <Container
      ref={(node: HTMLElement | null) => {
        root.current = node;
      }}
      className={`segmented-tabs ${className}`}
      role={navigation ? undefined : "tablist"}
      aria-label={label}
    >
      <div
        ref={viewport}
        className={`segmented-tabs-viewport ${trackClassName}`}
        onDragStart={(event) => event.preventDefault()}
        onWheel={stopScroll}
        onPointerDown={(event) => {
          suppressClick.current = false;
          if (event.pointerType !== "mouse") stopScroll();
          if (
            event.pointerType !== "mouse" ||
            event.button !== 0 ||
            event.currentTarget.scrollWidth <= event.currentTarget.clientWidth
          )
            return;
          drag.current = {
            pointer: event.pointerId,
            x: event.clientX,
            y: event.clientY,
            scroll: event.currentTarget.scrollLeft,
            active: false,
          };
        }}
        onPointerMove={(event) => {
          const current = drag.current;
          if (!current || current.pointer !== event.pointerId) return;
          const dx = event.clientX - current.x;
          if (!current.active) {
            if (
              Math.abs(dx) < 5 ||
              Math.abs(dx) <= Math.abs(event.clientY - current.y)
            )
              return;
            current.active = true;
            // Take over from the current animated position, rather than the
            // position captured before the drag threshold was reached.
            stopScroll();
            current.scroll = event.currentTarget.scrollLeft;
            event.currentTarget.setPointerCapture(event.pointerId);
            root.current?.setAttribute("data-dragging", "");
          }
          event.preventDefault();
          event.currentTarget.scrollLeft = current.scroll - dx;
        }}
        onPointerUp={(event) => stopDrag(event.pointerId)}
        onPointerCancel={(event) => stopDrag(event.pointerId)}
        onLostPointerCapture={(event) => stopDrag(event.pointerId)}
        onPointerLeave={(event) => {
          if (!drag.current?.active) stopDrag(event.pointerId);
        }}
        onClickCapture={(event) => {
          if (suppressClick.current && event.detail !== 0) {
            event.preventDefault();
            event.stopPropagation();
            suppressClick.current = false;
          }
        }}
      >
        <div ref={content} className="segmented-tabs-content">
          <span
            ref={indicator}
            className="segmented-tabs-indicator"
            aria-hidden="true"
          />
          {options.map((option) => (
            <button
              key={option.value}
              ref={(button) => {
                if (button) buttons.current.set(option.value, button);
                else buttons.current.delete(option.value);
              }}
              type="button"
              className={`segmented-tab ${option.value === value ? "selected" : ""}`}
              id={option.id}
              role={navigation ? undefined : "tab"}
              aria-controls={option.controls}
              aria-selected={navigation ? undefined : option.value === value}
              aria-current={
                navigation && option.value === value ? "page" : undefined
              }
              title={option.title}
              aria-label={option.accessibleLabel}
              tabIndex={option.value === value ? 0 : -1}
              disabled={option.disabled}
              onClick={() => {
                if (option.value !== value)
                  onChange(option.value, viewport.current?.scrollLeft || 0);
                else reveal(option.value);
              }}
              onKeyDown={(event) => {
                if (event.altKey || event.ctrlKey || event.metaKey) return;
                const available = options.filter((item) => !item.disabled);
                const at = available.indexOf(option);
                const direction =
                  getComputedStyle(event.currentTarget).direction === "rtl"
                    ? -1
                    : 1;
                const next =
                  event.key === "Home"
                    ? 0
                    : event.key === "End"
                      ? available.length - 1
                      : event.key === "ArrowLeft"
                        ? (at - direction + available.length) % available.length
                        : event.key === "ArrowRight"
                          ? (at + direction + available.length) %
                            available.length
                          : -1;
                if (next < 0 || !available[next]) return;
                event.preventDefault();
                const button = buttons.current.get(available[next].value);
                button?.focus({ preventScroll: true });
                if (available[next].value !== value)
                  onChange(
                    available[next].value,
                    viewport.current?.scrollLeft || 0,
                  );
                else reveal(available[next].value);
              }}
            >
              {option.icon}
              <span className="segmented-tab-label">{option.label}</span>
            </button>
          ))}
          {hasTrailing && (
            <div ref={trailingAction} className="segmented-tabs-trailing">
              {trailing}
            </div>
          )}
        </div>
      </div>
    </Container>
  );
}

type IndicatorPosition = {
  x: number;
  y: number;
  width: number;
  height: number;
};

function indicatorPosition(button: HTMLElement): IndicatorPosition {
  return {
    x: button.offsetLeft,
    y: button.offsetTop,
    width: button.offsetWidth,
    height: button.offsetHeight,
  };
}

function indicatorStyle(position: IndicatorPosition) {
  return {
    transform: `translate3d(${position.x}px, ${position.y}px, 0)`,
    width: `${position.width}px`,
    height: `${position.height}px`,
  };
}

function samePosition(a: IndicatorPosition, b: IndicatorPosition) {
  return (
    Math.abs(a.x - b.x) < 0.1 &&
    Math.abs(a.y - b.y) < 0.1 &&
    Math.abs(a.width - b.width) < 0.1 &&
    Math.abs(a.height - b.height) < 0.1
  );
}
