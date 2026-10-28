import { isMacDesktop } from "./runtime.js";

export function DesktopWindowChrome() {
  if (!isMacDesktop()) return null;
  return (
    <>
      <div className="desktop-window-drag-strip" aria-hidden="true" />
      <div className="desktop-window-brand" aria-hidden="true">
        <img src="/logo.png" alt="" draggable={false} />
      </div>
    </>
  );
}
