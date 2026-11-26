import { createRoot } from "react-dom/client";
import { ApplicationBoundary } from "./application-boundary";
import { SettingsApp } from "./settings-app";
import { DesktopWindowChrome } from "./desktop-window-chrome";
import "./style.css";
import "./settings.css";

createRoot(document.getElementById("root")!).render(
  <>
    <DesktopWindowChrome />
    <ApplicationBoundary>
      <SettingsApp />
    </ApplicationBoundary>
  </>,
);
