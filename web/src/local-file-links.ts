export type LocalFileTarget = { path: string; line?: number; column?: number };

export function localFileTarget(
  href: string,
  basePath = "",
): LocalFileTarget | null {
  let path = href.trim().replace(/^<|>$/g, "");
  if (
    !path ||
    path.startsWith("#") ||
    path.startsWith("//") ||
    /[\x00-\x1f]/.test(path)
  )
    return null;
  if (/^file:/i.test(path)) {
    try {
      const url = new URL(path);
      if (url.hostname && url.hostname !== "localhost") return null;
      path = url.pathname + url.hash;
      if (/^\/[A-Za-z]:\//.test(path)) path = path.slice(1);
    } catch {
      return null;
    }
  } else if (
    /^[a-z][a-z\d+.-]*:/i.test(path) &&
    !/^[a-z]:[\\/]/i.test(path) &&
    !/^[^/?#]+\.[a-z\d]+:\d+(?::\d+)?$/i.test(path)
  ) {
    return null;
  }
  try {
    path = decodeURIComponent(path);
  } catch {
    return null;
  }
  if (/[\x00-\x1f]/.test(path)) return null;
  if (
    /^(?:https?|javascript|data|vbscript|mailto|tel):/i.test(path) ||
    path.startsWith("//")
  )
    return null;
  const location = path.match(
    /(?:#L?(\d+)(?:C(\d+))?(?:-L?\d+)?|:(\d+)(?::(\d+))?)$/i,
  );
  const line = location ? Number(location[1] || location[3]) : undefined;
  const column = location
    ? Number(location[2] || location[4]) || undefined
    : undefined;
  if (location) path = path.slice(0, location.index);
  else path = path.replace(/#.*$/, "");
  path = path.replaceAll("\\", "/");
  if (!path || path.includes("://")) return null;
  if (basePath && !path.startsWith("/") && !/^[a-z]:\//i.test(path)) {
    path = basePath.slice(0, basePath.lastIndexOf("/") + 1) + path;
  }
  return {
    path,
    ...(line && line > 0 && line <= 1_000_000 ? { line } : {}),
    ...(column && column > 0 && column <= 1_000_000 ? { column } : {}),
  };
}

export function projectFileURL(
  project: string,
  path: string,
  mode?: "download" | "image",
) {
  const query = new URLSearchParams({ path });
  if (mode) query.set(mode, "1");
  return `/api/projects/${encodeURIComponent(project)}/file?${query}`;
}
