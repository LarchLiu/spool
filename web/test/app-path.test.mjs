import test from "node:test";
import assert from "node:assert/strict";
import { appBasePath, appURL, apiPath } from "../.test-build/app-path.js";

const first = `/web/${"a".repeat(52)}/`;
const second = `/web/${"b".repeat(52)}/`;

test("direct and relay URLs preserve encoded paths, queries and external resources", () => {
  const saved = globalThis.location;
  try {
    for (const base of ["/", first, second]) {
      globalThis.location = { pathname: base };
      assert.equal(appBasePath(), base);
      for (const path of ["/", "/api/me", "/api/files?path=a%2Fb%20%E4%B8%AD", "/assets/chunk.js", "/sw.js", "/api/desktop/view/?token=test"]) {
        assert.equal(appURL(path), base + path.slice(1));
        assert.equal(appURL(appURL(path)), appURL(path));
      }
      for (const path of ["https://example.test/file", "//example.test/file", "blob:example", "data:image/png;base64,AA", "./file"]) {
        assert.equal(appURL(path), path);
      }
      assert.equal(apiPath(appURL("/api/files?path=a%2Fb")), "/files?path=a%2Fb");
      assert.equal(apiPath("/api/files?path=a%2Fb"), "/files?path=a%2Fb");
    }
  } finally {
    if (saved === undefined) delete globalThis.location;
    else globalThis.location = saved;
  }
});

test("only a canonical server path selects a relay mount", () => {
  for (const path of ["/", "/desktop.html", "/web/short/", first.slice(0, -1), first.toUpperCase(), `/web/%61${"a".repeat(51)}/`]) {
    assert.equal(appBasePath(path), "/");
  }
  assert.equal(appBasePath(first + "api/me"), first);
});
