import assert from "node:assert/strict";
import path from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import ts from "typescript";

test("global types exclude unrelated ambient packages without losing imported React or Markdown types", () => {
  const root = fileURLToPath(new URL("..", import.meta.url));
  const config = ts.readConfigFile(
    path.join(root, "tsconfig.json"),
    ts.sys.readFile,
  );
  assert.equal(config.error, undefined);
  const parsed = ts.parseJsonConfigFileContent(
    config.config,
    ts.sys,
    root,
    undefined,
    path.join(root, "tsconfig.json"),
  );
  assert.deepEqual(parsed.errors, []);

  const probe = path.join(root, "src", "__type_resolution_probe__.tsx");
  const missing = "spool_missing_transitive_type";
  const typeRoot = path.join(root, "node_modules", "@types");
  const source = `
    import type { Root } from "mdast";
    import type { Components } from "react-markdown";
    import { createRoot } from "react-dom/client";
    import "./probe.css";
    const tree: Root = { type: "root", children: [] };
    const components: Components = { p: ({ children }) => <p>{children}</p> };
    const element: React.ReactElement = <main>{import.meta.env.MODE}</main>;
    createRoot(document.createElement("div")).render(element);
  `;

  function program(options) {
    const host = ts.createCompilerHost(options);
    const readFile = host.readFile.bind(host);
    const fileExists = host.fileExists.bind(host);
    const getDirectories = host.getDirectories.bind(host);
    const directoryExists = host.directoryExists.bind(host);
    // Simulate an incomplete unrelated @types directory without touching node_modules.
    host.readFile = (file) => (file === probe ? source : readFile(file));
    host.fileExists = (file) => file === probe || fileExists(file);
    host.getDirectories = (directory) =>
      directory === typeRoot
        ? [...getDirectories(directory), missing]
        : getDirectories(directory);
    host.directoryExists = (directory) =>
      directory === path.join(typeRoot, missing) || directoryExists(directory);
    return ts.createProgram([probe], options, host);
  }

  const implicit = program({ ...parsed.options, types: undefined });
  assert.ok(
    implicit
      .getOptionsDiagnostics()
      .some(
        (diagnostic) =>
          diagnostic.code === 2688 &&
          ts
            .flattenDiagnosticMessageText(diagnostic.messageText, "\n")
            .includes(missing),
      ),
  );

  const explicit = program(parsed.options);
  const diagnostics = ts.getPreEmitDiagnostics(explicit);
  assert.deepEqual(
    diagnostics.map((diagnostic) =>
      ts.flattenDiagnosticMessageText(diagnostic.messageText, "\n"),
    ),
    [],
  );
  assert.deepEqual(explicit.getAutomaticTypeDirectiveNames(), ["vite/client"]);
});
