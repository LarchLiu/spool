import { useEffect, useRef, useState, type DragEvent } from "react";
import { attachmentFiles, hasAttachmentFiles } from "./attachment-selection";
import { nativeAttachmentDrop } from "./native-attachment-drop";
import { isDesktopChat } from "./runtime";

export function useAttachmentDrop(
  enabled: boolean,
  onFiles: (files: File[]) => void,
  context: string,
  onError: (message: string) => void,
) {
  const ref = useRef<HTMLFormElement>(null);
  const current = useRef({ enabled, onFiles, context, onError });
  current.current = { enabled, onFiles, context, onError };
  const depth = useRef(0);
  const [hovering, setHovering] = useState(false);
  const [nativeHovering, setNativeHovering] = useState(false);
  const reset = () => {
    depth.current = 0;
    setHovering(false);
    setNativeHovering(false);
  };

  useEffect(() => {
    reset();
    window.spoolAttachmentDrop?.reset();
  }, [enabled, context]);
  useEffect(() => {
    if (!isDesktopChat()) return;
    const receiver = nativeAttachmentDrop({
      state: () => current.current,
      contains: (x, y) => {
        const element = document.elementFromPoint(x, y);
        return !!element && !!ref.current?.contains(element);
      },
      url: () => location.href,
      onFiles: (files) => current.current.onFiles(files),
      onError: (message) => current.current.onError(message),
      onHover: setNativeHovering,
    });
    window.spoolAttachmentDrop = receiver;
    window.addEventListener("drop", receiver.record, true);
    return () => {
      window.removeEventListener("drop", receiver.record, true);
      if (window.spoolAttachmentDrop === receiver)
        delete window.spoolAttachmentDrop;
    };
  }, []);
  useEffect(() => {
    // Track the whole page so the composer is advertised as soon as a file
    // enters the app, including over the sidebar or conversation history.
    const enter = (event: globalThis.DragEvent) => {
      if (!hasAttachmentFiles(event.dataTransfer)) return;
      depth.current++;
      setHovering(current.current.enabled);
    };
    const leave = (event: globalThis.DragEvent) => {
      if (!hasAttachmentFiles(event.dataTransfer) && !depth.current) return;
      depth.current = Math.max(0, depth.current - 1);
      if (!depth.current) setHovering(false);
    };
    // Dropping outside the composer must not navigate away from the draft.
    const guard = (event: globalThis.DragEvent) => {
      if (!hasAttachmentFiles(event.dataTransfer)) return;
      if (event.type === "dragover") setHovering(current.current.enabled);
      if (!event.defaultPrevented) {
        event.preventDefault();
        if (event.dataTransfer) event.dataTransfer.dropEffect = "none";
      }
      if (event.type === "drop") reset();
    };
    window.addEventListener("dragenter", enter);
    window.addEventListener("dragleave", leave);
    window.addEventListener("dragover", guard);
    window.addEventListener("drop", guard);
    window.addEventListener("dragend", reset);
    window.addEventListener("blur", reset);
    return () => {
      window.removeEventListener("dragenter", enter);
      window.removeEventListener("dragleave", leave);
      window.removeEventListener("dragover", guard);
      window.removeEventListener("drop", guard);
      window.removeEventListener("dragend", reset);
      window.removeEventListener("blur", reset);
    };
  }, []);

  const dragOver = (event: DragEvent<HTMLElement>) => {
    if (!hasAttachmentFiles(event.dataTransfer)) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = enabled ? "copy" : "none";
  };

  return {
    ref,
    active: enabled && (hovering || nativeHovering),
    events: {
      onDragEnter: dragOver,
      onDragOver: dragOver,
      onDrop(event: DragEvent<HTMLElement>) {
        reset();
        if (!hasAttachmentFiles(event.dataTransfer)) return;
        event.preventDefault();
        if (!enabled) return;
        const files = attachmentFiles(event.dataTransfer);
        if (files.length) onFiles(files);
      },
    },
  };
}
