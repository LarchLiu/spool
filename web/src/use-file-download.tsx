import { useEffect, useRef, useState } from "react";
import { LoaderCircle, X } from "lucide-react";
import type { Translator } from "./i18n";
import { isDesktopChat, subscribeDesktopDownload } from "./runtime";
import {
  fetchDownload,
  saveBrowserDownload,
  saveFilePicker,
  startNativeDownload,
} from "./file-download";

type Notice = {
  state: "downloading" | "saved" | "handed-off" | "failed" | "cancelled";
  name: string;
  path?: string;
  error?: string;
};

export function useFileDownload() {
  const [notice, setNotice] = useState<Notice | null>(null);
  const pending = useRef<{
    url: string;
    name: string;
    controller: AbortController;
  } | null>(null);
  useEffect(() => {
    const off = subscribeDesktopDownload((result) => {
      const current = pending.current;
      if (!current || result.url !== current.url) return;
      setNotice({ ...result, name: current.name });
      if (result.state !== "downloading") pending.current = null;
    });
    return () => {
      off();
      pending.current?.controller.abort();
      pending.current = null;
    };
  }, []);

  async function download(url: string, name: string, t: Translator) {
    if (pending.current) return;
    const current = {
      url: new URL(url, location.href).href,
      name,
      controller: new AbortController(),
    };
    pending.current = current;
    setNotice({ state: "downloading", name });
    const { signal } = current.controller;
    const desktop = isDesktopChat();
    try {
      if (desktop) {
        // Validate errors without buffering the file or changing native streaming.
        await fetchDownload(current.url, t, signal, "HEAD");
        signal.throwIfAborted();
        startNativeDownload(current.url, name);
      } else {
        const result = await saveBrowserDownload(
          current.url,
          name,
          t,
          signal,
          saveFilePicker()?.({ suggestedName: name }),
        );
        if (!signal.aborted) setNotice({ ...result, name });
      }
    } catch (error) {
      if (!signal.aborted)
        setNotice({
          state:
            error instanceof DOMException && error.name === "AbortError"
              ? "cancelled"
              : "failed",
          name,
          error: error instanceof Error ? error.message : undefined,
        });
      if (pending.current === current) pending.current = null;
    } finally {
      if (!desktop && pending.current === current) pending.current = null;
    }
  }
  return {
    download,
    downloading: notice?.state === "downloading",
    notice,
    dismiss: () => setNotice(null),
  };
}

export function FileDownloadStatus({
  notice,
  dismiss,
  t,
}: {
  notice: Notice | null;
  dismiss: () => void;
  t: Translator;
}) {
  if (!notice) return null;
  const message =
    notice.state === "downloading"
      ? t("Downloading {name}…", { name: notice.name })
      : notice.state === "saved"
        ? t("Downloaded to {path}", { path: notice.path! })
        : notice.state === "handed-off"
          ? t(
              "Sent {name} to your browser. Check its downloads for the save location.",
              { name: notice.name },
            )
          : notice.state === "cancelled"
            ? t("Download cancelled")
            : t("Could not download {name}: {error}", {
                name: notice.name,
                error: notice.error || t("Request failed"),
              });
  return (
    <div className="file-download-status">
      <p role={notice.state === "failed" ? "alert" : "status"}>
        {notice.state === "downloading" && (
          <LoaderCircle size={14} className="stop-spinner" />
        )}
        {message}
      </p>
      {notice.state !== "downloading" && (
        <button
          type="button"
          className="icon"
          aria-label={t("Dismiss")}
          onClick={dismiss}
        >
          <X size={14} />
        </button>
      )}
    </div>
  );
}
