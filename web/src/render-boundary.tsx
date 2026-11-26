import { Component, type ErrorInfo, type ReactNode } from "react";
import { reportDesktopRenderFailure } from "./runtime";

// Rich content is allowed to fail independently of the surrounding workspace.
// Keep the source available and leave drafts and live connections mounted.
export class RenderBoundary extends Component<
  { children: ReactNode; fallback: ReactNode; application?: boolean },
  { failed: boolean }
> {
  state = { failed: false };

  static getDerivedStateFromError() {
    return { failed: true };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("Spool rendering failed", error, info.componentStack);
    reportDesktopRenderFailure(
      error instanceof Error ? error.name : "Error",
      info.componentStack ?? "",
      this.props.application === true,
    );
  }

  render() {
    return this.state.failed ? this.props.fallback : this.props.children;
  }
}
