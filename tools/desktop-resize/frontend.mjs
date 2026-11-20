import { resolve, sep } from "node:path";

// Exercise the shipped React/CSS/WebKit path without opening a workspace,
// authenticating a real user, or connecting to agent providers.
export function startFrontendFixture() {
  const root = resolve("frontend");
  const parts = [], turns = [];
  for (let index = 0; index < 60; index++) {
    const start = parts.length;
    parts.push({ role: "user", kind: "text", text: `合成问题 ${index + 1}：请解释窗口横向缩放时的布局变化。` });
    parts.push({ role: "assistant", kind: "text", text: [
      `## 合成回答 ${index + 1}`,
      "这是一段用于测试文字换行的合成内容。Resizing the window changes paragraph wrapping, highlighted code, tables and the scroll position.",
      "- 第一项包含 **粗体** 和 `inline code`\n- 第二项包含更长的说明文本，用来改变消息的高度。",
      "```ts\n" + Array.from({ length: 14 }, (_, line) => `export const value${line} = compute(${index}, ${line}, "a long literal used for the resize fixture");`).join("\n") + "\n```",
      "| Name | Value | Notes |\n| --- | --- | --- |\n| alpha | 1 | first row |\n| beta | 2 | second row |",
    ].join("\n\n") });
    turns.push({ id: `turn-${index}`, backend: "codex", status: "completed", part_start: start, input_end: start + 1, part_end: start + 2 });
  }
  const chat = { id: "resize-fixture", title: "窗口缩放诊断 · 合成会话", project_id: "fixture", user_id: "fixture", backend: "codex", status: "completed", updated_at: 1, turns, transcript: { parts } };
  const workspace = { path: "/synthetic/fixture", kind: "main", branch: "fixture", available: true };
  const json = value => Response.json(value);
  const server = Bun.serve({
    hostname: "127.0.0.1", port: 0,
    async fetch(request) {
      const url = new URL(request.url), path = url.pathname;
      if (path === "/api/me") return json({ user: { id: "fixture", nickname: "Synthetic user" }, csrf: "fixture" });
      if (path === "/api/version") return json({ version: "resize-fixture" });
      if (path === "/api/profiles") return json({ revision: 1, people: [{ id: "fixture", nickname: "Synthetic user" }] });
      if (path === "/api/navigation") return json({ projects: [{ id: "fixture", name: "Synthetic project" }], chats: [{ ...chat, turns: [], transcript: { parts: [] } }] });
      if (path === "/api/agents") return json([{ backend: "codex", enabled: true, installed: true, catalog: { models: [] } }]);
      if (path === "/api/permissions") return json({ backend: "codex", default: "ask", modes: [{ id: "ask", available: true }] });
      if (path.endsWith("/workspace")) return json({ ...workspace, workspaces: [workspace] });
      if (path.endsWith("/events")) return new Response(`event: snapshot\ndata: ${JSON.stringify(chat)}\n\n`, { headers: { "content-type": "text/event-stream" } });
      if (path.endsWith("/files")) return json({ path: "", entries: [], truncated: false });
      if (path.startsWith("/api/chats/resize-fixture")) return json(chat);
      if (path.startsWith("/api/")) return json([]);
      const file = resolve(root, "." + (path === "/" ? "/index.html" : decodeURIComponent(path)));
      if (!file.startsWith(root + sep)) return new Response("Not found", { status: 404 });
      const asset = Bun.file(file);
      return await asset.exists() ? new Response(asset) : new Response("Not found", { status: 404 });
    },
  });
  return { url: `http://127.0.0.1:${server.port}/?chat=resize-fixture`, close: () => server.stop(true) };
}
