import { createRoot } from "react-dom/client";
import { SettingsApp } from "./settings-app";
import "./style.css";
import "./settings.css";

createRoot(document.getElementById("root")!).render(<SettingsApp />);
