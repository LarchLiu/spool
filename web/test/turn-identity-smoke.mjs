// Run after `npm --prefix web run build`, with TMPDIR inside an owned build/tmp run
// and CHROME_BIN pointing to a standalone headless Chromium executable.
// Uses only synthetic API data and an isolated Chrome profile; never a real home.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createServer } from "node:http";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { dirname, extname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
const output = join(root, "build/artifacts/turn-actors");
const chrome = process.env.CHROME_BIN;
assert.ok(
  chrome,
  "Set CHROME_BIN to a standalone headless Chromium executable",
);
assert.ok(
  process.env.TMPDIR?.startsWith(join(root, "build/tmp/")),
  "Set TMPDIR inside an owned build/tmp run",
);
const scratch = await mkdtemp(
  join(process.env.TMPDIR, "turn-identity-browser-"),
);
const people = [
  { id: "spool-bot", nickname: "Spool Bot", bot: true },
  { id: "bob", nickname: "Bob", avatar: { style: "thumbs", seed: "bob" } },
  { id: "alice", nickname: "Alice", avatar: { style: "clay", seed: "alice" } },
];
const now = Date.now() - 60_000;
const chat = {
  id: "demo-chat",
  project_id: "demo-project",
  user_id: "alice",
  title: "Issue #42 · Shared task",
  backend: "codex",
  status: "completed",
  updated_at: now,
  automation_chat: true,
  collaborator_ids: ["alice", "bob"],
  participants: people,
  transcript: {
    parts: [
      {
        role: "user",
        kind: "text",
        text: "检查 Issue #42，修复聊天中的用户归属。",
      },
      {
        role: "assistant",
        kind: "text",
        text: "已完成修复。自动任务归属 Spool Bot，手动跟进保留操作者身份。",
      },
      {
        role: "user",
        kind: "text",
        text: "请把操作者头像放在消息右侧，列表中叠加显示。",
      },
      {
        role: "assistant",
        kind: "text",
        text: "已补上头像和操作记录；历史轮次默认归属聊天创建者。",
      },
    ],
  },
  turns: [
    {
      id: "bot-turn",
      user_id: "spool-bot",
      backend: "codex",
      status: "completed",
      started_at: now - 30_000,
      finished_at: now - 20_000,
      part_start: 0,
      input_end: 1,
      part_end: 2,
    },
    {
      id: "human-turn",
      user_id: "bob",
      backend: "codex",
      status: "completed",
      started_at: now - 10_000,
      finished_at: now,
      part_start: 2,
      input_end: 3,
      part_end: 4,
      actions: [
        {
          id: "approval",
          user_id: "alice",
          kind: "approve",
          approval_id: "demo-approval",
          created_at: now - 5000,
        },
      ],
    },
  ],
};
const navigationChats = [
  chat,
  {
    ...chat,
    id: "single-operator",
    title: "Single operator",
    participants: [people[1]],
    updated_at: now - 1,
  },
  {
    ...chat,
    id: "no-operator-info",
    title: "No operator info",
    participants: undefined,
    updated_at: now - 2,
  },
  {
    ...chat,
    id: "empty-operators",
    title: "Empty operators",
    backend: "claude",
    participants: [],
    updated_at: now - 3,
  },
  {
    ...chat,
    id: "many-operators",
    title: "Many operators",
    participants: [...people, { id: "four", nickname: "Fourth" }],
    updated_at: now - 4,
  },
];
const reports = new Map();
const probe = `<style>.sidebar { transition: none !important; }</style><script>
const mode = new URL(location.href).searchParams.get('mode');
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
const pointer = (target, type, relatedTarget) => target.dispatchEvent(new PointerEvent(type, {bubbles:true, pointerType:'mouse', relatedTarget}));
const poll = setInterval(async () => {
  const avatars = [...document.querySelectorAll('.turn-operator-avatar')];
  if (avatars.length !== 2 || avatars.some(el => !el.querySelector('img')?.naturalWidth)) return;
  clearInterval(poll);
  const rows = [...document.querySelectorAll('.user-row')];
  const audit = document.querySelector('.turn-actions');
  const response = audit?.closest('.assistant');
  const footer = response?.querySelector('.message-footer-assistant');
  const report = {
    width:innerWidth, overflow:document.documentElement.scrollWidth > innerWidth,
    avatars:avatars.map(el=>el.getAttribute('aria-label')),
    messageAvatarSizesUnchanged:avatars.every(el=>el.querySelector('img').getBoundingClientRect().width===28),
    logo:!!document.querySelector('.turn-operator-avatar img[src="/logo.png"]'),
    stacks:document.querySelectorAll('.chat-operators').length,
    navIcons:[...document.querySelectorAll('.nav-chat')].map(el=>({
      title:el.querySelector('.nav-chat-title')?.textContent,
      leading:el.firstElementChild?.classList.contains('chat-operators') ? 'operators' : el.querySelector('.nav-chat-agent > .agent-icon') ? 'agent' : 'unknown',
      names:el.querySelector('.chat-operators')?.getAttribute('aria-label'),
      agent:el.querySelector('.agent-icon')?.getAttribute('aria-label'),
      avatars:el.querySelectorAll('.chat-operators img').length,
      avatarSizes:[...el.querySelectorAll('.chat-operators img')].map(img=>img.getBoundingClientRect().width),
      avatarBorders:[...el.querySelectorAll('.avatar-stack-item:not(.avatar-stack-more)')].map(item=>parseFloat(getComputedStyle(item).borderLeftWidth)),
      prefixWidth:el.querySelector('.chat-operators, .nav-chat-agent')?.getBoundingClientRect().width,
      prefixHeight:el.querySelector('.chat-operators, .nav-chat-agent')?.getBoundingClientRect().height,
      agentSize:el.querySelector('.agent-icon')?.getBoundingClientRect().width,
      agentCentered:!el.querySelector('.agent-icon') || el.querySelector('.agent-icon').getBoundingClientRect().left - el.querySelector('.nav-chat-agent').getBoundingClientRect().left === 1,
      nested:el.classList.contains('nested'),
      titleOffset:el.querySelector('.nav-chat-title').getBoundingClientRect().left - el.getBoundingClientRect().left,
      stackWidth:el.querySelector('.avatar-stack')?.getBoundingClientRect().width,
      more:el.querySelector('.avatar-stack-more')?.textContent,
      moreOverlap:parseFloat(getComputedStyle(el.querySelector('.avatar-stack-more') || el).marginLeft),
      titleFollowsIcon:el.querySelector('.chat-operators') ? el.children[1]?.classList.contains('nav-chat-open') && el.children[1]?.firstElementChild?.classList.contains('nav-chat-title') : el.querySelector('.nav-chat-open')?.children[1]?.classList.contains('nav-chat-title'),
      overlapping:[...el.querySelectorAll('.avatar-stack-item + .avatar-stack-item:not(.avatar-stack-more)')].every(item=>parseFloat(getComputedStyle(item).marginLeft)===-10)
    })),
    rowsFit:rows.every(el=>el.getBoundingClientRect().right <= innerWidth),
    avatarOnRight:rows.every(el=>el.querySelector('.turn-operator-avatar').getBoundingClientRect().left >= el.querySelector('.user-message').getBoundingClientRect().right),
    audit:audit?.textContent,
    auditAboveFooter:!!footer && !!(audit.compareDocumentPosition(footer) & Node.DOCUMENT_POSITION_FOLLOWING) && audit.getBoundingClientRect().bottom <= footer.getBoundingClientRect().top,
    auditAfterAnswer:!!response && response.querySelector('.assistant-text').getBoundingClientRect().bottom <= audit.getBoundingClientRect().top
  };
  try {
    if (mode === 'mobile') {
      document.querySelector('.panel-toggle-left').click();
      await pause(300);
    }
    const trigger = [...document.querySelectorAll('.recents .chat-operators')].find(el=>el.getAttribute('aria-label').includes('Fourth'));
    const panel = () => document.getElementById(trigger.getAttribute('aria-controls'));
    if (mode === 'mobile') trigger.click();
    else pointer(trigger, 'pointerover');
    await pause(50);
    const popup = panel();
    const rect = popup.getBoundingClientRect();
    report.members = {
      names:[...popup.querySelectorAll('li > span')].map(el=>el.textContent),
      avatars:popup.querySelectorAll('li img').length,
      avatarSizesUnchanged:[...popup.querySelectorAll('li img')].every(img=>img.getBoundingClientRect().width===28),
      botLogo:!!popup.querySelector('img[src="/logo.png"]'),
      inViewport:rect.left >= 0 && rect.top >= 0 && rect.right <= innerWidth && rect.bottom <= innerHeight,
      aboveTrigger:rect.bottom <= trigger.getBoundingClientRect().top,
      outsideScroller:!document.querySelector('.sidebar-scroll').contains(popup),
      visible:getComputedStyle(popup).visibility === 'visible',
      triggerVisible:trigger.getBoundingClientRect().left >= 0 && !trigger.closest('[inert]')
    };
    pointer(trigger, 'pointerout', popup);
    pointer(popup, 'pointerover', trigger);
    await pause(200);
    report.members.hoverable = panel() === popup;
    pointer(popup, 'pointerout', document.body);
    await pause(200);
    report.members.dismissedOnLeave = !panel();
    trigger.focus();
    await pause(50);
    report.members.keyboardOpen = !!panel();
    document.dispatchEvent(new KeyboardEvent('keydown', {key:'Escape',bubbles:true}));
    await pause(30);
    report.members.escapeDismissed = !panel();
    report.members.sidebarRetained = mode !== 'mobile' || document.querySelector('.sidebar').classList.contains('open');
    trigger.blur();
    trigger.click();
    await pause(30);
    report.members.clickOpen = !!panel();
    document.body.dispatchEvent(new PointerEvent('pointerdown', {bubbles:true,pointerType:'touch'}));
    await pause(30);
    report.members.outsideDismissed = !panel();
    trigger.click();
    await pause(30);
    panel().dispatchEvent(new Event('scroll'));
    await pause(30);
    report.members.ownScrollRetained = !!panel();
    document.querySelector('.sidebar-scroll').dispatchEvent(new Event('scroll'));
    await pause(30);
    report.members.sidebarScrollDismissed = !panel();
    report.members.noNestedButtons = !document.querySelector('button button');
    report.members.chatUnchanged = [...document.querySelectorAll('.nav-chat-open[aria-current="page"]')].every(el=>el.querySelector('.nav-chat-title').textContent === 'Issue #42 · Shared task');
    const topTrigger = document.querySelector('.project-chats .chat-operators');
    topTrigger.click();
    await pause(30);
    const topPopup = document.getElementById(topTrigger.getAttribute('aria-controls'));
    report.members.topEdgeFallback = topPopup.getBoundingClientRect().top >= topTrigger.getBoundingClientRect().bottom;
    document.body.dispatchEvent(new PointerEvent('pointerdown', {bubbles:true}));
    await pause(30);
    trigger.click();
    await pause(50);
  } catch (error) {
    report.memberError = String(error);
  }
  fetch('/smoke/report?mode='+mode, {method:'POST',body:JSON.stringify(report)});
}, 50);
</script>`;
const mime = {
  ".html": "text/html",
  ".css": "text/css",
  ".js": "text/javascript",
  ".png": "image/png",
  ".svg": "image/svg+xml",
  ".woff2": "font/woff2",
};
const server = createServer(async (req, res) => {
  const url = new URL(req.url, "http://localhost");
  const path = url.pathname;
  const json = (data) => {
    res.setHeader("Content-Type", "application/json");
    res.end(JSON.stringify(data));
  };
  if (path === "/smoke/report") {
    let body = "";
    for await (const chunk of req) body += chunk;
    reports.set(url.searchParams.get("mode"), JSON.parse(body));
    return json({ ok: true });
  }
  if (path === "/api/version") return json({ version: "smoke-fixture" });
  if (path === "/api/profiles")
    return json({
      revision: 1,
      people: [...people.slice(1), { id: "four", nickname: "Fourth" }],
    });
  if (path === "/api/me")
    return json({
      user: { ...people[2], username: "alice" },
      csrf: "synthetic",
      preferences: { theme: "light", language: "zh-CN" },
    });
  if (path === "/api/navigation")
    return json({
      projects: [{ id: "demo-project", name: "Spool", shared_chats: false }],
      chats: navigationChats.map((item) => ({
        ...item,
        turns: [],
        transcript: { parts: [] },
      })),
    });
  if (path === "/api/agents")
    return json([
      {
        backend: "codex",
        enabled: true,
        installed: true,
        catalog: {
          models: [
            {
              model: "demo",
              displayName: "Demo",
              defaultReasoningEffort: "medium",
              supportedReasoningEfforts: [],
            },
          ],
          defaultModel: "demo",
        },
      },
    ]);
  if (path === "/api/permissions")
    return json({
      backend: "codex",
      default: "auto",
      modes: [{ id: "auto", available: true }],
    });
  if (path === "/api/presence") return json([]);
  if (path.endsWith("/events")) {
    res.writeHead(200, {
      "Content-Type": "text/event-stream",
      "Cache-Control": "no-store",
    });
    res.end(
      `event: ${path.includes("/chats/") ? "snapshot" : "presence"}\ndata: ${JSON.stringify(path.includes("/chats/") ? chat : [])}\n\n`,
    );
    return;
  }
  if (path === "/api/chats/demo-chat") return json(chat);
  if (path.startsWith("/api/projects/"))
    return json({ path: "", entries: [], truncated: false });
  if (path.startsWith("/api/")) {
    res.statusCode = 404;
    return json({ error: "Unknown smoke fixture endpoint" });
  }
  try {
    assert.ok(!path.includes(".."));
    const file = path === "/" ? "index.html" : path.slice(1);
    let body = await readFile(join(root, "frontend", file));
    if (file === "index.html")
      body = Buffer.from(body.toString().replace("</body>", probe + "</body>"));
    res.setHeader(
      "Content-Type",
      mime[extname(file)] || "application/octet-stream",
    );
    res.end(body);
  } catch {
    res.statusCode = 404;
    res.end();
  }
});

try {
  await mkdir(output, { recursive: true });
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  for (const [mode, width, height] of [
    ["desktop", 1440, 1000],
    ["mobile", 390, 844],
  ]) {
    const url = `http://127.0.0.1:${server.address().port}/?mode=${mode}`;
    await new Promise((resolve, reject) => {
      const child = spawn(
        chrome,
        [
          "--headless",
          "--no-first-run",
          "--no-default-browser-check",
          "--disable-background-networking",
          "--disable-component-update",
          "--disable-sync",
          "--disable-extensions",
          "--disable-breakpad",
          `--user-data-dir=${join(scratch, mode)}`,
          `--window-size=${width},${height}`,
          "--force-device-scale-factor=1",
          "--hide-scrollbars",
          "--virtual-time-budget=5000",
          `--screenshot=${join(output, mode + ".png")}`,
          url,
        ],
        { stdio: ["ignore", "pipe", "pipe"] },
      );
      let diagnostics = "";
      for (const stream of [child.stdout, child.stderr]) {
        stream.on("data", (data) => {
          diagnostics = (diagnostics + data).slice(-16_000);
        });
      }
      const timeout = setTimeout(() => child.kill("SIGTERM"), 30_000);
      child.once("error", reject);
      child.once("close", async (code) => {
        clearTimeout(timeout);
        await writeFile(join(output, "chrome-" + mode + ".log"), diagnostics);
        code === 0
          ? resolve()
          : reject(new Error(`Chrome ${mode} exited ${code}`));
      });
    });
    const report = reports.get(mode);
    assert.ok(report, `No ${mode} rendering report`);
    assert.deepEqual(report.avatars, ["Spool Bot", "Bob"]);
    assert.ok(report.messageAvatarSizesUnchanged);
    assert.ok(
      report.logo &&
        report.stacks >= 2 &&
        report.rowsFit &&
        report.avatarOnRight &&
        !report.overflow,
      JSON.stringify(report),
    );
    assert.match(report.audit, /Alice/);
    assert.ok(report.auditAboveFooter && report.auditAfterAnswer);
    assert.equal(report.memberError, undefined);
    assert.deepEqual(report.members.names, [
      "Spool Bot",
      "Bob",
      "Alice",
      "Fourth",
    ]);
    assert.equal(report.members.avatars, 4);
    for (const [check, passed] of Object.entries(report.members)) {
      if (check !== "names" && check !== "avatars")
        assert.equal(passed, true, `${mode}: member popover ${check}`);
    }
    for (const [title, leading, avatars, identity] of [
      [chat.title, "operators", 3, "Spool Bot, Bob, Alice"],
      ["Single operator", "operators", 1, "Bob"],
      ["No operator info", "agent", 0, "Codex"],
      ["Empty operators", "agent", 0, "Claude"],
      ["Many operators", "operators", 3, "Spool Bot, Bob, Alice, Fourth"],
    ]) {
      const entries = report.navIcons.filter((item) => item.title === title);
      assert.equal(
        entries.length,
        2,
        `${mode}: both lists must include ${title}`,
      );
      for (const entry of entries) {
        assert.equal(entry.leading, leading, `${mode}: ${title}`);
        assert.equal(entry.avatars, avatars, `${mode}: ${title}`);
        assert.deepEqual(
          entry.avatarSizes,
          Array(avatars).fill(avatars === 1 ? 16 : 14),
        );
        assert.deepEqual(
          entry.avatarBorders,
          Array(avatars).fill(avatars === 1 ? 0 : 1),
        );
        assert.equal(entry.names || entry.agent, identity, `${mode}: ${title}`);
        assert.ok(entry.titleFollowsIcon && entry.overlapping);
        if (leading === "operators") assert.equal(entry.agent, undefined);
        if (entry.avatars === 3) {
          assert.equal(entry.stackWidth, title === "Many operators" ? 45 : 28);
          assert.equal(entry.prefixHeight, 16);
        } else {
          assert.equal(entry.prefixWidth, 16);
          assert.equal(entry.prefixHeight, 16);
          assert.equal(entry.titleOffset, entry.nested ? 57 : 34);
        }
        if (leading === "agent") {
          assert.equal(entry.agentSize, 14);
          assert.ok(entry.agentCentered);
        }
        if (entry.avatars === 1) assert.equal(entry.stackWidth, 16);
        if (title === "Many operators") {
          assert.equal(entry.more, "+1");
          assert.equal(entry.moreOverlap, -3);
        }
      }
    }
  }
  await writeFile(
    join(output, "browser-report.json"),
    JSON.stringify(Object.fromEntries(reports), null, 2) + "\n",
  );
  console.log(JSON.stringify(Object.fromEntries(reports), null, 2));
} finally {
  server.closeAllConnections();
  await new Promise((resolve) => server.close(resolve));
  await rm(scratch, { recursive: true, force: true });
}
