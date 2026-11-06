/** Copy text without disturbing the current focus or document selection. */
export async function copyMessageText(
  text: string,
  clipboard: Pick<Clipboard, "writeText"> | undefined = navigator.clipboard,
  doc: Document = document,
) {
  if (clipboard?.writeText) {
    try {
      await clipboard.writeText(text);
      return;
    } catch {
      // Plain HTTP deployments and embedded webviews may need the legacy API.
    }
  }
  const focused = doc.activeElement as HTMLElement | null;
  const selection = doc.getSelection();
  const ranges = selection
    ? Array.from({ length: selection.rangeCount }, (_, index) =>
        selection.getRangeAt(index).cloneRange(),
      )
    : [];
  const textarea = doc.createElement("textarea");
  textarea.value = text;
  textarea.readOnly = true;
  textarea.style.cssText =
    "position:fixed;top:0;left:0;opacity:0;pointer-events:none;font-size:16px";
  doc.body.append(textarea);
  try {
    textarea.focus({ preventScroll: true });
    textarea.select();
    if (!doc.execCommand("copy")) throw new Error("Clipboard copy failed");
  } finally {
    textarea.remove();
    focused?.focus({ preventScroll: true });
    if (selection) {
      selection.removeAllRanges();
      for (const range of ranges) selection.addRange(range);
    }
  }
}
