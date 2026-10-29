import type { Attachment } from "./types";

export type DraftAttachment = Attachment & { file: File };

export function pasteAttachments(
  event: {
    clipboardData: DataTransfer | null;
    preventDefault: () => void;
  },
  addFiles: (files: File[]) => void,
) {
  const clipboard = event.clipboardData;
  if (!clipboard) return;
  let files = Array.from(clipboard.files);
  // Some webviews expose pasted images only through clipboard items.
  if (!files.length) {
    files = Array.from(clipboard.items).flatMap((item) => {
      const file = item.kind === "file" ? item.getAsFile() : null;
      return file ? [file] : [];
    });
  }
  if (!files.length) return;
  event.preventDefault();
  addFiles(files);
}

export function validateAttachments(files: { size: number }[]): string | null {
  if (
    files.length > 6 ||
    files.reduce((total, file) => total + file.size, 0) > 16 * 1024 * 1024
  )
    return "Attach at most 6 files, up to 16 MB total";
  if (files.some((file) => !file.size || file.size > 4 * 1024 * 1024))
    return "Each attachment must be nonempty and at most 4 MB";
  return null;
}

export function attachmentURL(chatID: string, file: Attachment): string {
  return (
    file.preview_url ||
    `/api/chats/${encodeURIComponent(chatID)}/attachments?id=${encodeURIComponent(file.id)}`
  );
}

export function releaseAttachments(files: DraftAttachment[]) {
  for (const file of files)
    if (file.preview_url) URL.revokeObjectURL(file.preview_url);
}

export async function prepareAttachments(
  files: File[],
): Promise<DraftAttachment[]> {
  const excerpts = await Promise.all(
    files.map(async (file) =>
      /^(text\/|application\/(json|javascript|xml))/.test(file.type) ||
      /\.(json|md|txt|csv|go|rs|ts|tsx|js|py|yaml|yml|toml|log)$/i.test(
        file.name,
      )
        ? file.slice(0, 4096).text()
        : undefined,
    ),
  );
  return files.map((file, index) => ({
    id: crypto.randomUUID(),
    name: file.name,
    media_type: file.type || "application/octet-stream",
    size: file.size,
    file,
    preview_url: file.type.startsWith("image/")
      ? URL.createObjectURL(file)
      : undefined,
    excerpt: excerpts[index],
  }));
}
