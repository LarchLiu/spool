# Office file previews

Web, Desktop and native Android can preview `.docx` and `.xlsx` files in the
file panel. Referenced files in chat use the same preview when opened. Download
continues to return the original file.

The Go server parses the document and returns structured JSON. Word uses Go's
ZIP/XML and image packages; Excel uses `excelize/v2`. No LibreOffice executable,
browser document service or additional system package is required. Update the
server or Desktop application together with the clients; an older server does
not provide the Office preview API.

## Supported content

- Word: paragraphs, headings, basic bullets, bold/italic/underline, text colors,
  embedded raster images and tables with horizontal merged cells.
- Excel: visible worksheet selection, formatted saved cell values, basic font,
  fill and alignment, column widths, wrapped text and merged cells. A merged
  region crossing a page boundary repeats its value and style on the next page.
- Excel shows 50 rows per page, with previous/next controls and horizontal
  scrolling for wide sheets. Web also keeps row/column headers visible while
  scrolling. Android renders selectable native views without a WebView.

This is a read-only content preview. Word pagination, precise page layout,
headers/footers and complex drawing objects are not reproduced. Excel charts,
embedded objects and pivot-table interfaces are not rendered. Formulas are not
recalculated: the preview uses the workbook's saved results, or displays the
formula text when no result is available. Legacy `.doc`/`.xls` files and encrypted
Office files remain downloadable without an inline preview.

## Preview limits

Files are limited to 32 MiB compressed and 64 MiB of expanded ZIP content. Word
shows up to 2,000 body blocks and 256 KiB of text; embedded images are bounded and
converted to PNG thumbnails. Excel shows the first 128 columns, at most 4 KiB of
text per cell and 256 KiB per page. A notice identifies truncated previews;
the download action provides the full original file.

## API and verification

The existing authenticated file endpoints accept
`office=1&sheet=<zero-based sheet index>&row=<zero-based row offset>`:

```text
/api/projects/:id/file?path=report.docx&office=1
/api/chats/:id/file?path=book.xlsx&office=1&sheet=0&row=50
```

Project/session authorization, worktree scope and external-file settings apply
on every request. The preview reads the already-authorized open file; it does
not fetch external document images or execute document content.

Parser tests generate a shared JSON fixture consumed by both client test suites.
Run checks from the repository root using the repository's cache/scratch wrapper:

```sh
sh android/tools/run.sh go test ./internal/office ./internal/workspace
sh android/tools/run.sh bun run --bun --cwd web test
sh android/gradlew :app:testDebugUnitTest \
  --tests com.cloudgeek.spool.android.NativeOfficePreviewTest
```

After changing the JSON contract, regenerate the fixture and rerun both clients:

```sh
sh android/tools/run.sh env UPDATE_OFFICE_FIXTURES=1 go test ./internal/office
```

The browser smoke helper is `web/test/office-preview-smoke.mjs`; it needs
Playwright and an installed Chrome. Set `SPOOL_NODE_MODULES` if Playwright is
outside `web/node_modules`. It uses an isolated mock file API and writes its
latest report/screenshots to `build/artifacts/office-preview/`.
