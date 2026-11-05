import {
  Component,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { File, type FileOptions } from "@pierre/diffs/react";

type Props = {
  path: string;
  contents: string;
  line?: number;
  theme: "light" | "dark";
};

export function centerSourceLine(scroller: HTMLElement, row: Element) {
  const bounds = row.getBoundingClientRect();
  scroller.scrollTop +=
    bounds.top -
    scroller.getBoundingClientRect().top -
    (scroller.clientHeight - bounds.height) / 2;
}

export function PlainFileSource({
  contents,
  line,
}: Pick<Props, "contents" | "line">) {
  const source = useRef<HTMLPreElement>(null);
  useEffect(() => {
    const row = source.current?.querySelector(`[data-line="${line}"]`);
    const scroller = source.current?.closest<HTMLElement>(
      ".file-preview-scroll",
    );
    if (row && scroller) centerSourceLine(scroller, row);
  }, [line]);
  return (
    <pre className="file-source" ref={source}>
      {contents.split("\n").map((text, index) => (
        <span
          className={`file-source-line ${index + 1 === line ? "highlighted" : ""}`}
          data-line={index + 1}
          key={index}
        >
          <span className="file-line-number" aria-hidden="true">
            {index + 1}
          </span>
          <code>{text || " "}</code>
        </span>
      ))}
    </pre>
  );
}

export class FileSourceBoundary extends Component<
  { contents: string; line?: number; children: ReactNode },
  { failed: boolean }
> {
  state = { failed: false };

  static getDerivedStateFromError() {
    return { failed: true };
  }

  render() {
    return this.state.failed ? (
      <PlainFileSource contents={this.props.contents} line={this.props.line} />
    ) : (
      this.props.children
    );
  }
}

export function FileSource(props: Props) {
  return (
    <FileSourceBoundary
      key={props.path}
      contents={props.contents}
      line={props.line}
    >
      <HighlightedFileSource {...props} />
    </FileSourceBoundary>
  );
}

function HighlightedFileSource({ path, contents, line, theme }: Props) {
  const file = useMemo(() => ({ name: path, contents }), [path, contents]);
  const [rendered, setRendered] = useState<typeof file>();
  const highlighted = rendered === file;
  const container = useRef<HTMLDivElement>(null);
  const positioned = useRef<string | null>(null);
  useEffect(() => {
    if (!highlighted || !line) return;
    const node = container.current?.querySelector("diffs-container");
    const row = node?.shadowRoot?.querySelector(`[data-line="${line}"]`);
    const scroller = node?.closest<HTMLElement>(".file-preview-scroll");
    const target = `${path}:${line}`;
    if (row && scroller && positioned.current !== target) {
      centerSourceLine(scroller, row);
      positioned.current = target;
    }
  }, [highlighted, path, line]);
  const options = useMemo<FileOptions<undefined, undefined>>(
    () => ({
      disableFileHeader: true,
      disableErrorHandling: true,
      themeType: theme,
      overflow: "scroll",
      // Let the preview viewport own both axes so its horizontal scrollbar
      // stays reachable even when the file is taller than the viewport.
      unsafeCSS:
        "[data-code] { width: max-content; min-width: 100%; overflow: visible; contain: none; }",
      onPostRender: (node, _instance, phase) => {
        if (phase === "unmount") return;
        // The renderer mounts an empty container before its highlighter loads.
        // Async import failures are only logged, outside the React boundary.
        // Keep readable source until actual code has been rendered.
        const code = node.shadowRoot?.querySelector("code");
        if (code && (contents === "" || code.textContent)) setRendered(file);
      },
    }),
    [file, contents, theme],
  );
  return (
    <>
      {!highlighted && <PlainFileSource contents={contents} line={line} />}
      <div ref={container} hidden={!highlighted}>
        <File
          className="file-code-preview"
          file={file}
          options={options}
          selectedLines={line ? { start: line, end: line } : null}
          edit={false}
          disableWorkerPool
        />
      </div>
    </>
  );
}
