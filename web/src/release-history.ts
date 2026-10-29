import type { ReleaseHistory } from "./settings-client";

type State = {
  loading: boolean;
  history?: ReleaseHistory;
  error: string;
};

export class ReleaseHistoryLoader {
  private state: State = { loading: false, error: "" };
  private pending?: Promise<void>;
  private listeners = new Set<() => void>();

  constructor(
    private current: string,
    private target: string,
    private fetch: () => Promise<ReleaseHistory>,
  ) {}

  getState = () => this.state;
  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };

  private update(state: State) {
    this.state = state;
    for (const listener of this.listeners) listener();
  }

  load(refresh = false): Promise<void> {
    if (this.pending) return this.pending;
    if (this.state.history && !refresh) return Promise.resolve();
    this.update({ ...this.state, loading: true, error: "" });
    this.pending = Promise.resolve()
      .then(() => this.fetch())
      .then((history) => {
        if (
          history.current_version !== this.current ||
          history.target_version !== this.target
        )
          throw new Error(
            "The available update changed. Reopen release notes.",
          );
        this.update({ loading: false, error: "", history });
      })
      .catch((error: unknown) => {
        this.update({
          ...this.state,
          loading: false,
          error: error instanceof Error ? error.message : String(error),
        });
      })
      .finally(() => {
        this.pending = undefined;
      });
    return this.pending;
  }
}
