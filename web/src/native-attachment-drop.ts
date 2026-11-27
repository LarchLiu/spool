import { attachmentFiles } from "./attachment-selection";

export type NativeAttachmentDelivery = {
  token: string;
  url: string;
  files: { name: string; type: string; data: string; modified: number }[];
  error?: string;
};

// Native can only offer files supplied by an OS drop. This object provides no
// path-based file API to the page, including pages for remote servers.
export function nativeAttachmentDrop(target: {
  state: () => { enabled: boolean; context: string };
  contains: (x: number, y: number) => boolean;
  url: () => string;
  onFiles: (files: File[]) => void;
  onError: (message: string) => void;
  onHover: (active: boolean) => void;
}) {
  let pending:
    | { token: string; context: string; url: string; expires: number }
    | undefined;
  const domDrops: { x: number; y: number; at: number }[] = [];
  return {
    hover(event: { active: boolean; x: number; y: number }) {
      // Entering the native page advertises the composer before the pointer
      // reaches it. Only the completed drop is restricted to its bounds.
      target.onHover(event.active && target.state().enabled);
    },
    record(event: Pick<DragEvent, "dataTransfer" | "clientX" | "clientY">) {
      if (!event.dataTransfer || !attachmentFiles(event.dataTransfer).length)
        return;
      domDrops.push({ x: event.clientX, y: event.clientY, at: Date.now() });
      if (domDrops.length > 8) domDrops.shift();
    },
    claim(x: number, y: number): string {
      target.onHover(false);
      // MyGo reports both DOM drops and drops intercepted by the native surface.
      // Its DOM bridge posts during capture; this runs after that JS event has
      // finished. A DOM drop already took the ordinary attachment path.
      const now = Date.now();
      if (pending && now >= pending.expires) pending = undefined;
      while (domDrops.length && now - domDrops[0].at > 5000) domDrops.shift();
      const index = domDrops.findIndex(
        (drop) => Math.abs(drop.x - x) <= 1 && Math.abs(drop.y - y) <= 1,
      );
      if (index >= 0) {
        domDrops.splice(index, 1);
        return "";
      }
      const state = target.state();
      if (!state.enabled || pending || !target.contains(x, y)) return "";
      const token = crypto.randomUUID();
      pending = {
        token,
        context: state.context,
        url: target.url(),
        expires: now + 15000,
      };
      return token;
    },
    receive(delivery: NativeAttachmentDelivery): boolean {
      if (!pending || delivery.token !== pending.token) return false;
      const origin = pending;
      pending = undefined;
      const state = target.state();
      if (
        Date.now() >= origin.expires ||
        !state.enabled ||
        state.context !== origin.context ||
        delivery.url !== origin.url ||
        target.url() !== origin.url
      )
        return false;
      if (delivery.error) target.onError(delivery.error);
      else
        target.onFiles(
          delivery.files.map(
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
      return true;
    },
    reset() {
      pending = undefined;
      target.onHover(false);
    },
  };
}

export type NativeAttachmentDrop = ReturnType<typeof nativeAttachmentDrop>;

declare global {
  interface Window {
    spoolAttachmentDrop?: NativeAttachmentDrop;
  }
}
