import type {
  ServerSettings,
  SettingsCommand,
  SettingsSnapshot,
} from "./settings-client";

export type SettingsOperation =
  SettingsCommand | ((latest: SettingsSnapshot | undefined) => SettingsCommand);

export function createSettingsCommandQueue(
  execute: (command: SettingsCommand) => Promise<SettingsSnapshot | undefined>,
  current?: () => SettingsSnapshot | undefined,
) {
  let pending = Promise.resolve();
  let latest: SettingsSnapshot | undefined;
  return (operation: SettingsOperation) => {
    const result = pending.then(async () => {
      const next = await execute(
        typeof operation === "function"
          ? operation(current?.() ?? latest)
          : operation,
      );
      if (next) latest = next;
      return next;
    });
    pending = result.then(
      () => {},
      () => {},
    );
    return result;
  };
}

type ServiceDraft = {
  host: string;
  port: string;
  public_url: string;
  idle_minutes: string;
  history_cache_mib: string;
  auto_start: boolean;
  tsnet_enabled: boolean;
  tsnet_hostname: string;
  tsnet_port: string;
};
type ServiceField = keyof ServiceDraft;

function toDraft(settings: ServerSettings): ServiceDraft {
  return {
    host: settings.host,
    public_url: settings.public_url,
    auto_start: settings.auto_start,
    port: String(settings.port),
    idle_minutes: String(settings.idle_minutes),
    history_cache_mib: String(settings.history_cache_mib),
    tsnet_enabled: settings.tsnet_enabled ?? false,
    tsnet_hostname: settings.tsnet_hostname ?? "spool-server",
    tsnet_port: String(settings.tsnet_port ?? 443),
  };
}

export class ServiceSettingsAutosave {
  draft: ServiceDraft;
  private saved: ServerSettings;
  private edits: Partial<Record<ServiceField, number>> = {};
  private pendingFields: Partial<Record<ServiceField, number>> = {};

  constructor(
    settings: ServerSettings,
    private save: (
      settings: (latest?: ServerSettings) => ServerSettings,
    ) => Promise<ServerSettings | undefined>,
    private changed: () => void,
  ) {
    this.saved = { ...settings };
    this.draft = toDraft(settings);
  }

  edit<K extends ServiceField>(key: K, value: ServiceDraft[K]) {
    if (this.draft[key] === value) return;
    this.draft = { ...this.draft, [key]: value };
    this.edits[key] = (this.edits[key] ?? 0) + 1;
    this.changed();
  }

  sync(settings: ServerSettings, committed?: [ServiceField, number]) {
    const previous = toDraft(this.saved);
    const next = toDraft(settings);
    // Polling and save acknowledgements may update untouched fields, but must
    // not replace a newer edit or another field's incomplete input.
    const draft = Object.fromEntries(
      (Object.keys(next) as ServiceField[]).map((key) => [
        key,
        (!this.pendingFields[key] && this.draft[key] === previous[key]) ||
        (committed?.[0] === key && (this.edits[key] ?? 0) === committed[1])
          ? next[key]
          : this.draft[key],
      ]),
    ) as ServiceDraft;
    this.saved = { ...settings };
    if (
      (Object.keys(draft) as ServiceField[]).some(
        (key) => draft[key] !== this.draft[key],
      )
    ) {
      this.draft = draft;
      this.changed();
    }
  }

  async commit(key: ServiceField) {
    const raw = this.draft[key];
    const edit = this.edits[key] ?? 0;
    let value: string | number | boolean = raw;
    if (
      key === "port" ||
      key === "tsnet_port" ||
      key === "idle_minutes" ||
      key === "history_cache_mib"
    ) {
      value = Number(raw);
      if (
        !Number.isSafeInteger(value) ||
        value < 1 ||
        ((key === "port" || key === "tsnet_port") && value > 65535) ||
        (key === "idle_minutes" && value > 10080)
      )
        return;
    }
    if (!this.pendingFields[key] && value === this.saved[key]) return;
    // Build the full command when its turn arrives, so rapid edits to different
    // fields merge with the preceding save instead of restoring stale values.
    this.pendingFields[key] = (this.pendingFields[key] ?? 0) + 1;
    try {
      const next = await this.save((latest) => ({
        ...(latest ?? this.saved),
        [key]: value,
      }));
      if (next) this.sync(next, [key, edit]);
    } finally {
      this.pendingFields[key] = (this.pendingFields[key] ?? 1) - 1;
    }
  }
}
