import { useEffect, useRef } from "react";
import { Download, FileCode2, FileText, X } from "lucide-react";
import type { Attachment } from "./types";
import type { Translator } from "./i18n";
import { attachmentURL } from "./attachment-selection";

export function AttachmentCards({
  files,
  chatID = "",
  t,
  onRemove,
  onPreview,
}: {
  files: Attachment[];
  chatID?: string;
  t: Translator;
  onRemove?: (id: string) => void;
  onPreview: (file: Attachment, url: string) => void;
}) {
  return (
    <div className="attachment-cards" aria-label={t("Attachments")}>
      {files.map((file) => {
        const image = file.media_type.startsWith("image/");
        const url = attachmentURL(chatID, file);
        const code = /\.(json|go|rs|ts|tsx|js|py|yaml|yml|toml)$/i.test(
          file.name,
        );
        const contents = (
          <>
            {image ? (
              <img src={url} alt={file.name} />
            ) : (
              <>
                <div
                  className={file.excerpt ? "file-excerpt" : "file-placeholder"}
                >
                  {file.excerpt ? (
                    <pre>{file.excerpt}</pre>
                  ) : (
                    <FileText size={28} />
                  )}
                </div>
                <span className="attachment-name">
                  {code ? <FileCode2 size={14} /> : <FileText size={14} />}
                  <span>{file.name}</span>
                </span>
              </>
            )}
          </>
        );
        return (
          <div
            className={image ? "attachment-card image-card" : "attachment-card"}
            key={file.id}
          >
            {image ? (
              <button
                type="button"
                className="attachment-open"
                onClick={() => onPreview(file, url)}
                aria-label={t("Preview {name}", { name: file.name })}
              >
                {contents}
              </button>
            ) : onRemove ? (
              <div className="attachment-open" title={file.name}>
                {contents}
              </div>
            ) : (
              <a
                className="attachment-open"
                href={url}
                download={file.name}
                title={t("Download {name}", { name: file.name })}
              >
                {contents}
              </a>
            )}
            {onRemove && (
              <button
                type="button"
                className="attachment-remove"
                aria-label={t("Remove {name}", { name: file.name })}
                onClick={() => onRemove(file.id)}
              >
                <X size={12} />
              </button>
            )}
          </div>
        );
      })}
    </div>
  );
}

export function ImagePreview({
  file,
  url,
  t,
  onClose,
}: {
  file: Attachment;
  url: string;
  t: Translator;
  onClose: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    dialog.current?.showModal();
  }, []);
  return (
    <dialog
      className="image-preview"
      ref={dialog}
      aria-label={t("Preview {name}", { name: file.name })}
      onCancel={(event) => {
        event.preventDefault();
        onClose();
      }}
      onClick={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <div className="image-preview-toolbar">
        <span>{file.name}</span>
        <a
          className="icon"
          href={url}
          download={file.name}
          aria-label={t("Download {name}", { name: file.name })}
        >
          <Download size={18} />
        </a>
        <button
          autoFocus
          type="button"
          className="icon"
          onClick={onClose}
          aria-label={t("Close preview")}
        >
          <X size={20} />
        </button>
      </div>
      <img
        src={url}
        alt={file.name}
        onClick={(event) => event.stopPropagation()}
      />
    </dialog>
  );
}
