import { isDesktopChat } from "./runtime";

export type ScreenshotVisibility = "hide" | "show";
export type ScreenshotResult = {
  token: string;
  files?: { name: string; type: string; data: string; modified: number }[];
  error?: string;
};

export function screenshotURL(
  visibility: ScreenshotVisibility,
  token: string,
): string {
  return "/__desktop/screenshot?" + new URLSearchParams({ visibility, token });
}

export function screenshotSupported(surface: unknown = globalThis): boolean {
  return (
    isDesktopChat(surface) &&
    "mygo" in surface &&
    typeof surface.mygo === "object" &&
    surface.mygo !== null &&
    "platform" in surface.mygo &&
    ["darwin", "win32", "linux"].includes(String(surface.mygo.platform))
  );
}

// The request belongs to one draft. Native also checks the foreground WebView;
// this check covers SPA navigation, account changes, and a submitted draft.
export function screenshotReceiver(target: {
  state: () => { enabled: boolean; context: string };
  onFiles: (files: File[]) => void;
  onError: (message: string) => void;
  onBusy: (busy: boolean) => void;
}) {
  let pending: { token: string; context: string } | undefined;
  return {
    start(token: string): boolean {
      const state = target.state();
      if (!state.enabled || pending) return false;
      pending = { token, context: state.context };
      target.onBusy(true);
      return true;
    },
    receive(result: ScreenshotResult): boolean {
      if (!pending || result.token !== pending.token) return false;
      const origin = pending;
      pending = undefined;
      target.onBusy(false);
      const state = target.state();
      if (!state.enabled || state.context !== origin.context) return false;
      if (result.error) target.onError(result.error);
      else if (result.files?.length) {
        try {
          target.onFiles(
            result.files.map(
              (file) =>
                new File(
                  [
                    Uint8Array.from(atob(file.data), (value) =>
                      value.charCodeAt(0),
                    ),
                  ],
                  file.name,
                  { type: file.type, lastModified: file.modified },
                ),
            ),
          );
        } catch {
          target.onError("Could not read the screenshot");
        }
      }
      return true;
    },
    reset() {
      pending = undefined;
      target.onBusy(false);
    },
  };
}
