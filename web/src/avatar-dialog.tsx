import { useEffect, useMemo, useRef, useState } from "react";
import { Check, RefreshCw, X } from "lucide-react";
import {
  avatarChoices,
  avatarAttribution,
  avatarConfig,
  avatarName,
  avatarStyles,
  type AvatarConfig,
} from "./avatar";
import { UserAvatar } from "./user-avatar";
import type { Translator } from "./i18n";
import type { User } from "./types";

export function AvatarDialog({
  user,
  t,
  onSave,
  onClose,
}: {
  user: User;
  t: Translator;
  onSave: (avatar: AvatarConfig) => Promise<void>;
  onClose: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [draft, setDraft] = useState(() => avatarConfig(user));
  const [page, setPage] = useState(0);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const name = avatarName(user);
  const choices = useMemo(() => avatarChoices(name, page), [name, page]);
  const current = avatarConfig(user);
  const attribution = avatarAttribution(draft.style);
  const changed = current.style !== draft.style || current.seed !== draft.seed;
  useEffect(() => {
    dialog.current?.showModal();
  }, []);

  async function save() {
    setSaving(true);
    setError("");
    try {
      await onSave(draft);
      onClose();
    } catch (error) {
      setError(
        error instanceof Error ? error.message : t("Could not save avatar"),
      );
    } finally {
      setSaving(false);
    }
  }

  return (
    <dialog
      className="avatar-dialog"
      ref={dialog}
      aria-labelledby="avatar-title"
      onCancel={(event) => {
        event.preventDefault();
        if (!saving) onClose();
      }}
      onClick={(event) => {
        if (event.target !== event.currentTarget || saving) return;
        const r = event.currentTarget.getBoundingClientRect();
        if (
          event.clientX < r.left ||
          event.clientX > r.right ||
          event.clientY < r.top ||
          event.clientY > r.bottom
        )
          onClose();
      }}
    >
      <div className="dialog-heading">
        <h2 id="avatar-title">{t("Choose your avatar")}</h2>
        <button
          type="button"
          className="icon"
          autoFocus
          disabled={saving}
          aria-label={t("Close avatar picker")}
          onClick={onClose}
        >
          <X size={19} />
        </button>
      </div>
      <div className="avatar-dialog-body">
        <div className="avatar-preview">
          <UserAvatar person={{ ...user, avatar: draft }} size={80} />
          <div>
            <strong>{name}</strong>
            <p>
              {draft.seed ? t("Picked by you") : t("Generated from your name")}
            </p>
          </div>
        </div>
        <fieldset disabled={saving}>
          <legend>{t("Avatar style")}</legend>
          <div className="avatar-styles">
            {avatarStyles.map(([style, label]) => (
              <button
                key={style}
                type="button"
                aria-pressed={draft.style === style}
                onClick={() => setDraft({ ...draft, style })}
              >
                <UserAvatar
                  person={{ ...user, avatar: { style, seed: "" } }}
                  size={38}
                />
                <span>{t(label)}</span>
              </button>
            ))}
          </div>
          <button
            type="button"
            className="avatar-automatic"
            aria-pressed={!draft.seed}
            onClick={() => setDraft({ ...draft, seed: "" })}
          >
            <UserAvatar
              person={{ ...user, avatar: { ...draft, seed: "" } }}
              size={36}
            />
            <span>
              {t("Use my name")}
              <small>{t("Updates automatically when your name changes")}</small>
            </span>
            {!draft.seed && <Check size={16} />}
          </button>
          <div className="avatar-gallery-heading">
            <span>{t("Or pick one you like")}</span>
            <button
              type="button"
              className="quiet"
              onClick={() => setPage((page) => page + 1)}
            >
              <RefreshCw size={13} />
              {t("More avatars")}
            </button>
          </div>
          <div className="avatar-grid" aria-label={t("Avatar choices")}>
            {choices.map((seed, index) => (
              <button
                key={seed}
                type="button"
                aria-label={t("Avatar {number}", {
                  number: page * 12 + index + 1,
                })}
                aria-pressed={draft.seed === seed}
                onClick={() => setDraft({ ...draft, seed })}
              >
                <UserAvatar
                  person={{ ...user, avatar: { ...draft, seed } }}
                  size={48}
                />
                {draft.seed === seed && (
                  <Check className="selected-check" size={13} />
                )}
              </button>
            ))}
          </div>
        </fieldset>
        <p className="avatar-credit">
          {t("Generated on your device. Your name is never sent to DiceBear.")}{" "}
          <a
            href="https://www.dicebear.com/"
            target="_blank"
            rel="noopener noreferrer"
          >
            DiceBear
          </a>
          <span className="avatar-attribution">
            <a
              href={attribution.source.url}
              target="_blank"
              rel="noopener noreferrer"
            >
              {attribution.source.name}
            </a>
            {" / "}
            <a
              href={attribution.creator.url}
              target="_blank"
              rel="noopener noreferrer"
            >
              {attribution.creator.name}
            </a>
            {" / "}
            <a
              href={attribution.license.url}
              target="_blank"
              rel="noopener noreferrer"
            >
              {attribution.license.name}
            </a>
          </span>
        </p>
      </div>
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      <div className="avatar-dialog-actions">
        <button
          type="button"
          className="quiet"
          disabled={saving}
          onClick={onClose}
        >
          {t("Cancel")}
        </button>
        <button
          type="button"
          className="primary"
          disabled={saving || !changed}
          onClick={() => void save()}
        >
          {saving ? t("Working…") : t("Save avatar")}
        </button>
      </div>
    </dialog>
  );
}
