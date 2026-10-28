import { Component, useEffect, useMemo, useRef, type ReactNode } from "react";
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
  const positioned = useRef<string | null>(null);
  const options = useMemo<FileOptions<undefined, undefined>>(
    () => ({
      disableFileHeader: true,
      disableErrorHandling: true,
      themeType: theme,
      overflow: "scroll",
      onPostRender: (node, _instance, phase) => {
        const target = `${path}:${line}`;
        if (phase === "unmount" || !line || positioned.current === target)
          return;
        const row = node.shadowRoot?.querySelector(`[data-line="${line}"]`);
        const scroller = node.closest<HTMLElement>(".file-preview-scroll");
        if (row && scroller && scroller.clientHeight > 0) {
          // Highlighting is asynchronous; navigate once the requested row exists.
          centerSourceLine(scroller, row);
          positioned.current = target;
        }
      },
    }),
    [path, line, theme],
  );
  return (
    <File
      className="file-code-preview"
      file={file}
      options={options}
      selectedLines={line ? { start: line, end: line } : null}
      edit={false}
      disableWorkerPool
    />
  );
}
