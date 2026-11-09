import { useEffect, useId, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { ArrowUpRight, QrCode, X } from "lucide-react";
import type { Translator } from "./i18n";
import { settingsCall } from "./settings-client";

function browserAddress(address: string) {
  try {
    const url = new URL(address);
    return ["https:", "http:"].includes(url.protocol) &&
      !url.username &&
      !url.password
      ? address.trim()
      : "";
  } catch {
    return "";
  }
}

export function AddressLink({
  address,
  t,
  disabled = false,
}: {
  address: string;
  t: Translator;
  disabled?: boolean;
}) {
  const url = disabled ? "" : browserAddress(address);
  return (
    <a
      className="icon settings-address-link"
      href={url || undefined}
      target="_blank"
      rel="noopener noreferrer"
      aria-label={t("Open address")}
      title={t("Open address")}
      aria-disabled={!url || undefined}
      tabIndex={url ? undefined : -1}
    >
      <ArrowUpRight size={16} aria-hidden="true" />
    </a>
  );
}

export function AddressQRButton({
  address,
  title,
  t,
  disabled = false,
}: {
  address: string;
  title: string;
  t: Translator;
  disabled?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const trigger = useRef<HTMLButtonElement>(null);
  const label = t("Show address QR code");
  return (
    <>
      <button
        ref={trigger}
        type="button"
        className="icon settings-address-qr"
        aria-label={label}
        title={label}
        aria-haspopup="dialog"
        disabled={disabled || !browserAddress(address)}
        onClick={() => setOpen(true)}
      >
        <QrCode size={16} aria-hidden="true" />
      </button>
      {open &&
        createPortal(
          <AddressQRDialog
            key={address}
            address={address}
            title={title}
            t={t}
            onClose={() => {
              setOpen(false);
              trigger.current?.focus({ preventScroll: true });
            }}
          />,
          document.body,
        )}
    </>
  );
}

function AddressQRDialog({
  address,
  title,
  t,
  onClose,
}: {
  address: string;
  title: string;
  t: Translator;
  onClose: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const titleID = useId();
  const [image, setImage] = useState("");
  const [error, setError] = useState("");
  useEffect(() => {
    dialog.current?.showModal();
    let active = true;
    settingsCall<string>("AddressQRCode", address).then(
      (value) => {
        if (active) setImage(value);
      },
      () => {
        if (active) setError(t("Could not generate the address QR code."));
      },
    );
    return () => {
      active = false;
    };
  }, [address, t]);
  return (
    <dialog
      className="settings-address-qr-dialog"
      ref={dialog}
      aria-labelledby={titleID}
      onCancel={(event) => {
        event.preventDefault();
        onClose();
      }}
      onClick={(event) => {
        if (event.target !== event.currentTarget) return;
        const bounds = event.currentTarget.getBoundingClientRect();
        if (
          event.clientX < bounds.left ||
          event.clientX > bounds.right ||
          event.clientY < bounds.top ||
          event.clientY > bounds.bottom
        )
          onClose();
      }}
    >
      <div className="dialog-heading">
        <h2 id={titleID}>{title}</h2>
        <button
          type="button"
          autoFocus
          className="icon"
          aria-label={t("Close")}
          onClick={onClose}
        >
          <X size={18} aria-hidden="true" />
        </button>
      </div>
      <div className="settings-address-qr-image" aria-busy={!image && !error}>
        {image ? (
          <img
            src={image}
            width={256}
            height={256}
            alt={t("Address QR code")}
          />
        ) : (
          <p role={error ? "alert" : "status"}>{error || t("Working…")}</p>
        )}
      </div>
      <p className="settings-address-qr-url">{address}</p>
      <p className="settings-address-qr-hint">
        {t("Scan to open Spool in your browser.")}
      </p>
    </dialog>
  );
}
