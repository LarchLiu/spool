import { useEffect, useState, createElement, type CSSProperties } from "react";
import { ChevronLeft, ChevronRight } from "lucide-react";
import type { Translator } from "./i18n";
import { CustomSelect } from "./custom-select.js";

export type OfficeStyle = { bold?: boolean; italic?: boolean; underline?: boolean; color?: string; background?: string; align?: string };
export type OfficeCell = { text: string; style?: OfficeStyle; col_span?: number; row_span?: number; hidden?: boolean };
export type OfficeRun = { text?: string; image?: string; alt?: string; style?: OfficeStyle };
export type OfficeBlock = { kind: string; level?: number; align?: string; runs?: OfficeRun[]; rows?: OfficeCell[][] };
export type OfficeDocument = {
  kind: "word" | "spreadsheet";
  blocks?: OfficeBlock[];
  sheets?: { index: number; name: string }[];
  sheet: number;
  columns?: number[];
  rows?: { number: number; height?: number; cells: OfficeCell[] }[];
  row_start: number;
  has_more: boolean;
  truncated: boolean;
};

function style(value?: OfficeStyle): CSSProperties {
  const color = (v?: string) => v && /^#[\da-f]{6}$/i.test(v) ? v : undefined;
  return {
    fontWeight: value?.bold ? 700 : undefined,
    fontStyle: value?.italic ? "italic" : undefined,
    textDecoration: value?.underline ? "underline" : undefined,
    color: color(value?.color), backgroundColor: color(value?.background),
    textAlign: ["left", "center", "right", "justify"].includes(value?.align || "") ? value?.align as CSSProperties["textAlign"] : undefined,
  };
}

export function officeColumn(index: number): string {
  let name = "";
  for (let n = index + 1; n > 0; n = Math.floor((n - 1) / 26)) name = String.fromCharCode(65 + (n - 1) % 26) + name;
  return name;
}

function Cells({ cells }: { cells: OfficeCell[] }) {
  return cells.map((cell, index) => cell.hidden ? null : (
    <td key={index} style={style(cell.style)} colSpan={cell.col_span || 1} rowSpan={cell.row_span || 1}>{cell.text}</td>
  ));
}

export function OfficePage({ document, t }: { document: OfficeDocument; t: Translator }) {
  if (document.kind === "word") return (
    <article className="office-document" aria-label={t("Word preview")}>
      {document.blocks?.map((block, index) => block.kind === "table" ? (
        <div className="office-table-scroll" key={index}><table className="office-table"><tbody>
          {block.rows?.map((cells, row) => <tr key={row}><Cells cells={cells} /></tr>)}
        </tbody></table></div>
      ) : createElement(block.level ? `h${Math.max(1, Math.min(6, block.level))}` : "p", { key: index, style: style({ align: block.align }) },
        block.runs?.map((run, r) => run.image && /^data:image\/png;base64,[A-Za-z0-9+/=]+$/.test(run.image)
          ? <img key={r} src={run.image} alt={run.alt || ""} />
          : <span key={r} style={style(run.style)}>{run.text}</span>))) }
    </article>
  );
  if (!document.rows?.length) return <p className="file-panel-empty">{t("This worksheet is empty.")}</p>;
  return (
    <table className="office-table office-sheet" aria-label={t("Excel preview")}>
      <colgroup><col style={{ width: 48 }} />{document.columns?.map((width, col) => <col key={col} style={{ width }} />)}</colgroup>
      <thead><tr><th aria-label={t("Row")} />{document.columns?.map((_, col) => <th key={col} scope="col">{officeColumn(col)}</th>)}</tr></thead>
      <tbody>{document.rows.map(row => <tr key={row.number} style={{ height: row.height }}>
        <th scope="row">{row.number}</th><Cells cells={row.cells} />
      </tr>)}</tbody>
    </table>
  );
}

export function OfficePreview({ path, read, t }: {
  path: string;
  read: (path: string, signal: AbortSignal) => Promise<OfficeDocument>;
  t: Translator;
}) {
  const [document, setDocument] = useState<OfficeDocument | null>(null);
  const [sheets, setSheets] = useState<OfficeDocument["sheets"]>([]);
  const [sheet, setSheet] = useState(0), [row, setRow] = useState(0), [retry, setRetry] = useState(0);
  const [error, setError] = useState("");
  useEffect(() => {
    const controller = new AbortController(); setDocument(null); setError("");
    read(`${path}&office=1&sheet=${sheet}&row=${row}`, controller.signal)
      .then(value => {
        if (controller.signal.aborted) return;
        if (value.kind !== "word" && value.kind !== "spreadsheet") throw new Error(t("This Office file cannot be previewed. Download it to open locally."));
        setDocument(value); setSheets(value.sheets || []);
      })
      .catch(() => { if (!controller.signal.aborted) setError(t("This Office file cannot be previewed. Download it to open locally.")); });
    return () => controller.abort();
  }, [path, read, sheet, row, retry, t]);
  return <div className="office-preview" aria-busy={!document && !error}>
    {!!sheets?.length && <div className="office-toolbar">
      <CustomSelect label={t("Worksheet")} value={String(document?.sheet ?? sheet)} disabled={!document}
        options={sheets.map(s => ({ value: String(s.index), label: s.name }))}
        onChange={value => { setSheet(Number(value)); setRow(0); }} />
      <button className="icon" aria-label={t("Previous rows")} disabled={!document || row === 0} onClick={() => setRow(Math.max(0, row - 50))}><ChevronLeft size={15} /></button>
      <span>{document?.rows?.length ? t("Rows {start}–{end}", { start: document.row_start + 1, end: document.row_start + document.rows.length }) : ""}</span>
      <button className="icon" aria-label={t("Next rows")} disabled={!document?.has_more} onClick={() => { if (document) { setSheet(document.sheet); setRow(document.row_start + (document.rows?.length || 50)); } }}><ChevronRight size={15} /></button>
    </div>}
    <div className="office-scroll" key={`${sheet}:${row}`}>
      {error ? <p className="file-panel-empty" role="alert">{error} <button onClick={() => setRetry(retry + 1)}>{t("Retry")}</button></p>
        : document ? <OfficePage document={document} t={t} /> : <p className="file-panel-empty" role="status">{t("Loading file…")}</p>}
    </div>
    {document?.truncated && <p className="file-panel-notice">{t("Preview truncated. Download the full file to see all content.")}</p>}
  </div>;
}
