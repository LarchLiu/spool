import { createContext } from "react";
import type { LocalFileTarget } from "./local-file-links";

export const LocalFileContext = createContext<{
  openFile?: (target: LocalFileTarget) => void;
  basePath?: string;
}>({});
