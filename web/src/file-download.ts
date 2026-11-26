import type { Translator } from "./i18n";

type SaveHandle = {
  name: string;
  createWritable: () => Promise<WritableStream<Uint8Array>>;
};
type SavePicker = (options: { suggestedName: string }) => Promise<SaveHandle>;

export function saveFilePicker(): SavePicker | undefined {
  const picker = (window as unknown as { showSaveFilePicker?: SavePicker })
    .showSaveFilePicker;
  return picker?.bind(window);
}

export async function fetchDownload(
  url: string,
  t: Translator,
  signal: AbortSignal,
  method = "GET",
) {
  const response = await fetch(url, {
    method,
    signal,
    credentials: "same-origin",
    cache: "no-store",
    headers: { "X-UI-Language": document.documentElement.lang },
  });
  if (!response.ok) {
    if (response.status === 401)
      window.dispatchEvent(new Event("auth-expired"));
    const data = await response.json().catch(() => null);
    throw new Error(
      typeof data?.error === "string"
        ? data.error
        : `${t("Request failed")} (${response.status})`,
    );
  }
  return response;
}

export function startNativeDownload(url: string, name: string) {
  const link = document.createElement("a");
  link.href = url;
  link.download = name;
  document.body.append(link);
  try {
    link.click();
  } finally {
    link.remove();
  }
}

export async function saveBrowserDownload(
  url: string,
  name: string,
  t: Translator,
  signal: AbortSignal,
  handle?: Promise<SaveHandle>,
): Promise<{ state: "saved" | "handed-off"; path: string }> {
  // The picker must be opened synchronously in the click's user activation.
  const selected = await handle;
  if (selected) {
    const response = await fetchDownload(url, t, signal);
    if (!response.body)
      throw new Error(t("The server returned an invalid response"));
    // pipeTo closes on success and aborts the partial write on transfer failure.
    await response.body.pipeTo(await selected.createWritable(), { signal });
    return { state: "saved", path: selected.name };
  }
  // Keep large files streaming through the browser's download manager. Without
  // a save API, it owns completion/disk errors and does not expose the final path.
  await fetchDownload(url, t, signal, "HEAD");
  signal.throwIfAborted();
  startNativeDownload(url, name);
  return { state: "handed-off", path: name };
}
