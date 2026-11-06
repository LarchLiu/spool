import type { Attachment, TranscriptPart } from "./types";

export type TurnExchange = { start: number; inputEnd: number; end: number };

// A provider turn may contain steering messages between assistant/tool parts.
// Preserve transcript order; only adjacent blocks of the same input may merge.
export function turnExchanges(parts: readonly TranscriptPart[]): TurnExchange[] {
  const exchanges: TurnExchange[] = [];
  let current: TurnExchange = { start: 0, inputEnd: 0, end: 0 };
  let response = false, inputID = "", inputTime = 0;
  exchanges.push(current);
  parts.forEach((part, index) => {
    if (part.role === "user") {
      if (response || (inputID && part.id && inputID !== part.id) || (inputTime && part.timestamp && inputTime !== part.timestamp)) {
        current = { start: index, inputEnd: index, end: index };
        exchanges.push(current);
        response = false;
        inputID = "";
        inputTime = 0;
      }
      current.inputEnd = index + 1;
      if (part.id) inputID = part.id;
      if (part.timestamp) inputTime = part.timestamp;
    } else response = true;
    current.end = index + 1;
  });
  return exchanges;
}

export function exchangeFiles(parts: readonly TranscriptPart[], turnID: string, chatID: string, start: number): Attachment[] {
  return parts.flatMap((part, index) => {
    if (part.role !== "user") return [];
    if (part.kind === "image" && part.image_url) return [{
      id: `${turnID}-image-${start + index}`, name: part.name || "image",
      media_type: part.image_url.slice(5, part.image_url.indexOf(";")),
      size: 0, preview_url: part.image_url,
    }];
    if (part.kind === "context" && part.name && part.file_path) return [{
      id: `${turnID}-file-${start + index}`, name: part.name,
      media_type: "application/octet-stream", size: 0,
      file_path: part.file_path,
      preview_url: `/api/chats/${encodeURIComponent(chatID)}/file?path=${encodeURIComponent(part.file_path)}&download=1`,
    }];
    return [];
  });
}
