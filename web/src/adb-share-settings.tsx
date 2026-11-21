import { useEffect, useState, useSyncExternalStore } from "react";
import { Smartphone } from "lucide-react";
import {
  createADBShare,
  type ADBShare,
  type ADBShareAvailability,
  type ADBShareState,
} from "./adb-share";
import type { USBLike } from "./adb-usb";
import { api, getCSRF } from "./api";
import { appURL } from "./app-path";
import type { MessageKey, Translator } from "./i18n";
import { ConnectionControl } from "./connection-control";

export type ADBShareSupport = "supported" | "insecure" | "unsupported";

function browserUSB(): USBLike | undefined {
  return typeof navigator === "undefined"
    ? undefined
    : (navigator as { usb?: USBLike }).usb;
}

let shared: ADBShare | undefined;

// The page's one share outlives its popup and ends with the session that
// was allowed to start it.
function pageShare(): ADBShare | undefined {
  if (shared) return shared;
  const usb = browserUSB();
  if (!usb) return undefined;
  shared = createADBShare({
    usb,
    socket() {
      const url = new URL(appURL("/api/adb/bridge"), window.location.href);
      url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
      return new WebSocket(url);
    },
    csrf: getCSRF,
    availability: () => api<ADBShareAvailability>("/adb"),
  });
  window.addEventListener("auth-expired", () => shared?.stop());
  return shared;
}

const idle: ADBShareState = { phase: "idle" };
const never = () => () => {};

function useADBShareState(): ADBShareState {
  const share = pageShare();
  return useSyncExternalStore(
    share ? share.subscribe : never,
    share ? share.state : () => idle,
    () => idle,
  );
}

function useADBShareAvailability() {
  const [availability, setAvailability] = useState<ADBShareAvailability | null>(
    null,
  );
  useEffect(() => {
    const controller = new AbortController();
    let loading = false;
    const refresh = () => {
      if (loading || document.visibilityState === "hidden") return;
      loading = true;
      void api<ADBShareAvailability>("/adb", undefined, controller.signal)
        .then((next) => {
          if (controller.signal.aborted) return;
          setAvailability((old) =>
            old?.available === next.available && old?.reason === next.reason
              ? old
              : next,
          );
        })
        .catch(() => {
          if (!controller.signal.aborted) setAvailability(null);
        })
        .finally(() => {
          loading = false;
        });
    };
    refresh();
    const timer = setInterval(refresh, 5000);
    window.addEventListener("focus", refresh);
    document.addEventListener("visibilitychange", refresh);
    return () => {
      controller.abort();
      clearInterval(timer);
      window.removeEventListener("focus", refresh);
      document.removeEventListener("visibilitychange", refresh);
    };
  }, []);
  return availability;
}

const failures: Record<string, MessageKey> = {
  revoked: "Your access to the ADB bridge ended.",
  not_authorized: "Your access to the ADB bridge ended.",
  disabled: "The ADB bridge is turned off on the server.",
  adb_missing: "adb is not installed on the server.",
  unavailable: "adb is not installed on the server.",
  replaced: "This phone is now shared from another page.",
  busy: "The server cannot take another device right now. Stop sharing one, or try again shortly.",
  connect_failed: "The server's adb could not connect to the phone.",
  usb_busy:
    "The browser could not open the phone's USB debugging. Another program on this computer may be using it: close Android Studio and run adb kill-server, then try again.",
  usb: "The browser did not allow access to the USB device.",
  reselect_device:
    "The browser could not identify the same phone. Choose it again to resume sharing.",
};

// Failures whose text already says everything the diagnostic would.
const explained = new Set([
  "revoked",
  "not_authorized",
  "disabled",
  "adb_missing",
  "unavailable",
  "replaced",
  "busy",
]);

// One line for where sharing stands; empty while nothing is shared.
export function adbShareStatus(state: ADBShareState, t: Translator): string {
  switch (state.phase) {
    case "idle":
      return "";
    case "choosing":
      return t(
        "Choose the phone in the browser's device list. A phone appears there once USB debugging is turned on.",
      );
    case "connecting":
      return t("Connecting…");
    case "unauthorized":
      return t("Allow USB debugging on the phone to continue.");
    case "online":
      return t("The phone is shared as {serial}.", {
        serial: state.serial ?? "",
      });
    case "waiting":
      return t(
        state.code === "device_gone"
          ? "The phone was disconnected. Plug it in again to keep sharing."
          : "The connection to the server was interrupted. Reconnecting…",
      );
    case "failed":
      return t(
        failures[state.code ?? ""] ?? "Sharing the phone stopped unexpectedly.",
      );
  }
}

export function ADBShareSection({
  availability,
  support,
  state,
  t,
  onStart,
  onStop,
}: {
  // Unknown while the server has not answered.
  availability: ADBShareAvailability | null;
  support: ADBShareSupport;
  state: ADBShareState;
  t: Translator;
  onStart: () => void;
  onStop: () => void;
}) {
  const stopped = state.phase === "idle" || state.phase === "failed";
  // Nothing to offer without the grant. A share that is under way or has just
  // ended is still reported.
  if (
    state.phase === "idle" &&
    (!availability || availability.reason === "not_authorized")
  )
    return null;
  const usable = availability?.available === true;
  const status = adbShareStatus(state, t);
  const detail =
    state.phase === "failed" && !explained.has(state.code ?? "")
      ? state.detail
      : undefined;
  return (
    <section
      className="notification-settings"
      aria-labelledby="adb-share-title"
      data-adb-share={state.phase}
    >
      <h3 id="adb-share-title">
        <Smartphone size={15} aria-hidden="true" />
        {t("ADB bridge")}
      </h3>
      <p>
        {t(
          "Share an Android phone connected to this computer by USB with adb on the server, so agents there can install and debug apps on it. While it is shared, the server and every agent and program on it can control the phone.",
        )}
      </p>
      {state.phase === "failed" ? (
        <p className="notification-error" role="alert">
          {status}
        </p>
      ) : (
        status && <p role="status">{status}</p>
      )}
      {detail && <p>{detail}</p>}
      {state.phase === "online" && (
        <p>
          {t(
            "Agents on the server use it with adb -s {serial}. Keep this page open: sharing ends when it closes.",
            { serial: state.serial ?? "" },
          )}
        </p>
      )}
      {!stopped ? (
        state.phase !== "choosing" && (
          <div className="notification-actions">
            <button type="button" onClick={onStop}>
              {t("Stop sharing")}
            </button>
          </div>
        )
      ) : usable && support === "supported" ? (
        <div className="notification-actions">
          <button type="button" onClick={onStart}>
            {t("Share a phone over USB")}
          </button>
        </div>
      ) : (
        // A failure already says why sharing cannot start again.
        state.phase === "idle" && (
          <p>
            {t(
              !usable
                ? availability?.reason === "adb_missing"
                  ? "adb is not installed on the server."
                  : "The ADB bridge is turned off on the server."
                : support === "insecure"
                  ? "Sharing a phone requires HTTPS or localhost."
                  : "This browser cannot access USB devices. Use Chrome or Edge on a computer.",
            )}
          </p>
        )
      )}
    </section>
  );
}

export function ADBShareControl({
  t,
  active,
}: {
  t: Translator;
  active: boolean;
}) {
  if (!browserUSB()) return null;
  return <SupportedADBShareControl t={t} active={active} />;
}

function SupportedADBShareControl({
  t,
  active,
}: {
  t: Translator;
  active: boolean;
}) {
  const availability = useADBShareAvailability();
  const state = useADBShareState();
  const share = pageShare();
  if (
    availability?.reason === "disabled" ||
    (state.phase === "idle" &&
      (!availability || availability.reason === "not_authorized"))
  )
    return null;
  return (
    <ConnectionControl
      active={active}
      label={t("ADB bridge")}
      icon={<Smartphone size={16} aria-hidden="true" />}
      status={
        state.phase === "online"
          ? "connected"
          : state.phase === "connecting" ||
              state.phase === "unauthorized" ||
              state.phase === "waiting"
            ? "pending"
            : undefined
      }
      statusLabel={adbShareStatus(state, t)}
    >
      {() => (
        <ADBShareSection
          availability={availability}
          support={
            share
              ? "supported"
              : window.isSecureContext
                ? "unsupported"
                : "insecure"
          }
          state={state}
          t={t}
          onStart={() => void share?.start()}
          onStop={() => share?.stop()}
        />
      )}
    </ConnectionControl>
  );
}
