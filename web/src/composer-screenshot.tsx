import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type MouseEvent,
} from "react";
import { ChevronDown, LoaderCircle, ScanLine } from "lucide-react";
import type { Translator } from "./i18n";
import {
  screenshotReceiver,
  screenshotSupported,
  screenshotURL,
  type ScreenshotResult,
} from "./native-screenshot";

type ComposerScreenshotProps = {
  enabled: boolean;
  context: string;
  onFiles: (files: File[]) => void;
  onError: (message: string) => void;
  onComplete: () => void;
  t: Translator;
};

export function ComposerScreenshot(props: ComposerScreenshotProps) {
  return screenshotSupported() ? <NativeComposerScreenshot {...props} /> : null;
}

function NativeComposerScreenshot(props: ComposerScreenshotProps) {
  const current = useRef(props);
  current.current = props;
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [token, setToken] = useState(() => crypto.randomUUID());
  const root = useRef<HTMLDivElement>(null);
  const button = useRef<HTMLButtonElement>(null);
  const timeout = useRef<ReturnType<typeof setTimeout>>(undefined);
  const [receiver] = useState(() =>
    screenshotReceiver({
      state: () => ({
        enabled: current.current.enabled,
        context: current.current.context,
      }),
      onFiles: (files) => current.current.onFiles(files),
      onError: (message) => current.current.onError(message),
      onBusy: (value) => {
        setBusy(value);
        if (!value) {
          clearTimeout(timeout.current);
          setToken(crypto.randomUUID());
        }
      },
    }),
  );
  useLayoutEffect(() => {
    receiver.reset();
    setOpen(false);
  }, [receiver, props.context, props.enabled]);
  useEffect(() => {
    let focusFrame = 0;
    const receive = (event: Event) => {
      const result = (event as CustomEvent<ScreenshotResult>).detail;
      if (
        result &&
        typeof result.token === "string" &&
        receiver.receive(result)
      ) {
        const context = current.current.context;
        // Native restores the main window before delivering the result. Wait
        // for React to commit the attachment, then return typing to this draft.
        focusFrame = requestAnimationFrame(() => {
          if (current.current.context === context) current.current.onComplete();
        });
      }
    };
    window.addEventListener("spool:screenshot", receive);
    return () => {
      window.removeEventListener("spool:screenshot", receive);
      clearTimeout(timeout.current);
      cancelAnimationFrame(focusFrame);
    };
  }, [receiver]);
  useEffect(() => {
    if (!open) return;
    root.current
      ?.querySelector<HTMLAnchorElement>(".screenshot-options a")
      ?.focus();
    const close = (event: PointerEvent) => {
      if (!root.current?.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener("pointerdown", close);
    return () => document.removeEventListener("pointerdown", close);
  }, [open]);
  const { t } = props;
  const start = (event: MouseEvent<HTMLAnchorElement>) => {
    // Use a clicked link so the native navigation carries the user gesture.
    if (
      event.button !== 0 ||
      event.metaKey ||
      event.ctrlKey ||
      event.shiftKey ||
      event.altKey ||
      !receiver.start(token)
    ) {
      event.preventDefault();
      return;
    }
    setOpen(false);
    timeout.current = setTimeout(() => {
      receiver.receive({
        token,
        error: "The screenshot timed out. Try again.",
      });
    }, 125_000);
  };
  return (
    <div
      className="composer-screenshot"
      ref={root}
      onKeyDown={(event) => {
        if (event.key === "Escape" && open) {
          event.preventDefault();
          event.stopPropagation();
          setOpen(false);
          button.current?.focus();
        }
        if (open && (event.key === "ArrowDown" || event.key === "ArrowUp")) {
          event.preventDefault();
          const links = Array.from(
            root.current?.querySelectorAll<HTMLAnchorElement>(
              ".screenshot-options a",
            ) || [],
          );
          const index = links.indexOf(
            document.activeElement as HTMLAnchorElement,
          );
          links[
            (index + (event.key === "ArrowDown" ? 1 : links.length - 1)) %
              links.length
          ]?.focus();
        }
      }}
      onBlur={(event) => {
        // WebKit can briefly report no relatedTarget during a pointer click.
        // Wait for focus to settle so the option survives until its click.
        const container = event.currentTarget;
        requestAnimationFrame(() => {
          if (!container.contains(document.activeElement)) setOpen(false);
        });
      }}
    >
      <a
        href={screenshotURL("hide", token)}
        role="button"
        className="icon attachment-add"
        aria-label={t("Take screenshot")}
        title={t(busy ? "Taking screenshot…" : "Take screenshot")}
        aria-busy={busy || undefined}
        aria-disabled={!props.enabled || busy || undefined}
        tabIndex={!props.enabled || busy ? -1 : 0}
        onClick={start}
        onKeyDown={(event) => {
          if (event.key === " ") {
            event.preventDefault();
            event.currentTarget.click();
          }
        }}
      >
        {busy ? (
          <LoaderCircle size={18} className="stop-spinner" />
        ) : (
          <ScanLine size={18} />
        )}
      </a>
      <button
        ref={button}
        type="button"
        className="icon screenshot-mode"
        aria-label={t("Screenshot options")}
        title={t("Screenshot options")}
        aria-expanded={open}
        disabled={!props.enabled || busy}
        onClick={() => setOpen(!open)}
      >
        <ChevronDown size={12} />
      </button>
      <div
        className="screenshot-options"
        role="group"
        aria-label={t("Screenshot options")}
        hidden={!open}
      >
        {/* Keep the clicked anchor mounted until its native navigation runs. */}
        <a
          href={screenshotURL("show", token)}
          onMouseDown={(event) => {
            // WKWebView blurs a focused link on mouse down. Preserve focus so
            // onBlur cannot close the menu before mouse up delivers its click.
            event.preventDefault();
          }}
          onClick={start}
        >
          {t("Show Spool")}
        </a>
      </div>
    </div>
  );
}
