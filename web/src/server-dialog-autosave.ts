import type { DesktopServerProfile } from "./server-dialog-client";

export const serverEditableFields = [
  "name",
  "direct",
  "tailscale",
  "relay",
  "enabled",
  "automatic",
] as const;
type Field = (typeof serverEditableFields)[number];

// Save fields in order against the last acknowledgement, preserving incomplete
// input in other fields and edits made while a native request is in flight.
export class ServerDialogAutosave {
  draft: DesktopServerProfile;
  private saved: DesktopServerProfile;
  private generation = 0;
  private queue = Promise.resolve();
  private pending = new Map<Field, number>();
  private queued = new Map<Field, DesktopServerProfile[Field]>();

  constructor(
    profile: DesktopServerProfile,
    private save: (
      profile: DesktopServerProfile,
    ) => Promise<DesktopServerProfile | undefined>,
    private changed: (profile: DesktopServerProfile) => void,
  ) {
    this.draft = this.saved = profile;
  }

  reset(profile: DesktopServerProfile) {
    this.generation++;
    this.queue = Promise.resolve();
    this.pending.clear();
    this.queued.clear();
    this.draft = this.saved = profile;
    this.changed(profile);
  }

  edit<K extends Field>(key: K, value: DesktopServerProfile[K]) {
    this.draft = { ...this.draft, [key]: value };
    this.changed(this.draft);
  }

  sync(
    profile: DesktopServerProfile,
    committed?: Field,
    value?: DesktopServerProfile[Field],
  ) {
    const draft = { ...profile };
    for (const key of serverEditableFields) {
      if (
        (this.pending.has(key) || this.draft[key] !== this.saved[key]) &&
        !(key === committed && this.draft[key] === value)
      )
        Object.assign(draft, { [key]: this.draft[key] });
    }
    this.saved = profile;
    this.draft = draft;
    this.changed(draft);
  }

  commit(key: Field) {
    const value = this.draft[key];
    if (!this.pending.has(key) && value === this.saved[key]) return;
    if (this.queued.has(key) && this.queued.get(key) === value) return;
    const generation = this.generation;
    this.pending.set(key, (this.pending.get(key) ?? 0) + 1);
    this.queued.set(key, value);
    this.queue = this.queue
      .then(async () => {
        if (generation !== this.generation) return;
        try {
          const next = await this.save({ ...this.saved, [key]: value });
          if (next && generation === this.generation)
            this.sync(next, key, value);
        } finally {
          if (generation === this.generation) {
            const remaining = (this.pending.get(key) ?? 1) - 1;
            if (remaining) this.pending.set(key, remaining);
            else {
              this.pending.delete(key);
              this.queued.delete(key);
            }
          }
        }
      })
      .catch(() => {});
  }

  async settled() {
    // Another blur can enqueue work while a previous request is completing.
    for (;;) {
      const pending = this.queue;
      await pending;
      if (pending === this.queue) break;
    }
  }

  async flush(valid: (key: Field) => boolean = () => true) {
    for (const key of serverEditableFields) {
      if (valid(key)) this.commit(key);
    }
    await this.settled();
    return serverEditableFields.every(
      (key) => this.draft[key] === this.saved[key],
    );
  }
}
