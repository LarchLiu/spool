import { locale, t } from "./presentation.js";
(() => {
  const $ = (id) => document.getElementById(id);
  let credential = "";
  let settings;
  let timer;
  let generation = 0;
  let pending;
  let settingsDirty = false;
  let savingSettings = false;
  let issuedTokens = [];
  let batchResult = false;
  let tokenMode = "single";
  let creating = false;
  let registrations = [];
  let tokenResult = { total: 0, pages: 1, counts: {}, metrics: {} };
  const selectedRecords = new Map();
  let detailEntry;
  let listBusy = false;
  let listError = false;
  let listRevision = 0;
  let activityPage = 1;
  let activityResult = { events: [], page: 1, pages: 1, total: 0 };
  let currentPage = "servers";
  let purgeEntries = [];

  let tokenFilter = "registered";
  let tokenPage = 1;
  const tokenPageSize = 20;
  const selectedTokens = new Set();
  let pageTokens = [];
  let bulkBusy = false;
  let detailsID = "";
  let detailCopyMessage = "";
  let bulkAction;
  let revokeEntry;
  let notice;
  const tokenTabs = [...document.querySelectorAll("[data-token-filter]")];
  const emptyStates = {
    registered:
      "暂无已注册的 token。服务器使用 token 完成首次注册后会显示在这里。",
    revoked: "暂无已撤销或已注销的 token。",
    unused: "暂无未注册的 token。生成新 token 后会显示在这里。",
    all: "暂无 token。生成第一个 token 开始使用。",
  };
  const narrowSidebar = matchMedia("(max-width: 700px)");
  function setSidebar(open, restoreFocus = false) {
    const wasOpen = $("dashboard").classList.contains("sidebar-open");
    const expanded = open && narrowSidebar.matches;
    $("dashboard").classList.toggle("sidebar-open", expanded);
    $("sidebar-toggle").setAttribute("aria-expanded", String(expanded));
    $("sidebar-backdrop").hidden = !expanded;
    $("settings-sidebar").inert = narrowSidebar.matches && !expanded;
    $("settings-main").inert = $("mobile-header").inert = expanded;
    if (narrowSidebar.matches)
      $("settings-sidebar").setAttribute("aria-hidden", String(!expanded));
    else $("settings-sidebar").removeAttribute("aria-hidden");
    if (expanded) {
      $("settings-sidebar").setAttribute("role", "dialog");
      $("settings-sidebar").setAttribute("aria-modal", "true");
      $("sidebar-close").focus();
    } else {
      $("settings-sidebar").removeAttribute("role");
      $("settings-sidebar").removeAttribute("aria-modal");
      if (wasOpen && restoreFocus && narrowSidebar.matches)
        $("sidebar-toggle").focus();
    }
  }
  $("sidebar-toggle").addEventListener("click", () => setSidebar(true));
  $("sidebar-close").addEventListener("click", () => setSidebar(false, true));
  $("sidebar-backdrop").addEventListener("click", () =>
    setSidebar(false, true),
  );
  $("settings-sidebar").addEventListener("keydown", (event) => {
    if (!$("dashboard").classList.contains("sidebar-open")) return;
    if (event.key === "Escape") {
      event.preventDefault();
      setSidebar(false, true);
    } else if (event.key === "Tab") {
      const focusable = [
        ...$("settings-sidebar").querySelectorAll("a, button"),
      ].filter((element) => !element.disabled && element.checkVisibility());
      const target = event.shiftKey ? focusable.at(-1) : focusable[0];
      if (
        document.activeElement ===
        (event.shiftKey ? focusable[0] : focusable.at(-1))
      ) {
        event.preventDefault();
        target.focus();
      }
    }
  });
  $("dashboard-preferences").addEventListener(
    "click",
    () => setSidebar(false, true),
    { capture: true },
  );
  narrowSidebar.addEventListener("change", () => setSidebar(false));
  setSidebar(false);

  const local =
    location.hostname === "localhost" ||
    location.hostname === "[::1]" ||
    /^127\.\d+\.\d+\.\d+$/.test(location.hostname);
  if (location.protocol !== "https:" && !local) {
    $("login-button").disabled = true;
    showNotice(
      "请使用 HTTPS 管理网址，或通过 SSH 将管理端口转发到本机后访问。管理员 token 需要安全的连接。",
    );
  }

  function showNotice(message, success = false, values = {}) {
    notice = { message, success, values };
    ($("dashboard").hidden ? $("login-notice") : $("dashboard-notice")).append(
      $("notice"),
    );
    $("notice").textContent = t(message, values);
    $("notice").hidden = !message;
    $("notice").className = success ? "success" : "";
  }
  function logout() {
    setSidebar(false);
    generation++;
    credential = "";
    settings = undefined;
    clearTimeout(timer);
    $("admin-token").value = "";
    issuedTokens = [];
    batchResult = false;
    renderIssuedTokens();
    tokenFilter = "registered";
    tokenPage = 1;
    selectedTokens.clear();
    selectedRecords.clear();
    detailEntry = undefined;
    tokenResult = { total: 0, pages: 1, counts: {}, metrics: {} };
    listRevision++;
    listBusy = false;
    listError = false;
    activityPage = 1;
    activityResult = { events: [], page: 1, pages: 1, total: 0 };
    renderActivity();
    purgeEntries = [];
    $("purge-dialog").close();
    $("purge-confirmation").value = "";
    for (const id of [
      "purge-items",
      "purge-description",
      "bulk-dialog-items",
      "bulk-dialog-description",
      "revoke-description",
    ])
      $(id).textContent = "";
    bulkBusy = false;
    bulkAction = undefined;
    revokeEntry = undefined;
    detailsID = "";
    detailCopyMessage = "";
    $("token-details-name").textContent = $("detail-copy-status").textContent =
      "";
    for (const field of $("token-details").querySelectorAll(
      "dd > code, dd > a, dd[id]",
    ))
      field.textContent = "";
    $("detail-web-url").removeAttribute("href");
    resetTokenFilters();
    renderServers([]);
    $("server-name").value = "";
    $("dashboard").hidden = true;
    $("logout").hidden = true;
    $("login").hidden = false;
    navigate("servers");
    $("revoke-dialog").close();
    $("appearance-dialog").close();
    $("bulk-dialog").close();
    $("token-details").close();
    $("refresh").removeAttribute("aria-busy");
    pending = undefined;
  }
  async function api(path, options = {}) {
    const epoch = generation;
    const response = await fetch(path, {
      ...options,
      credentials: "omit",
      cache: "no-store",
      redirect: "error",
      headers: {
        Authorization: "Bearer " + credential,
        ...(options.body ? { "Content-Type": "application/json" } : {}),
        ...options.headers,
      },
    });
    if (epoch !== generation) throw new Error("操作已取消。");
    if (response.status === 401) {
      logout();
      throw new Error("管理员 token 无效，请重新输入。");
    }
    const data = response.status === 204 ? null : await response.json();
    if (epoch !== generation) throw new Error("操作已取消。");
    if (!response.ok) throw new Error(data?.error || "请求失败，请稍后重试。");
    return data;
  }
  function renderSettings(next, fill = false) {
    settings = next;
    $("detected-proxy").textContent =
      next.detected_proxy ||
      t(next.detected_at ? "直接 TLS 连接，无需代理 IP" : "尚无");
    $("detect").disabled = !next.public_url || next.detecting || savingSettings;
    $("detect").textContent = t(
      next.waiting_for_proxy
        ? "等待代理…"
        : next.detecting
          ? "检测中…"
          : "检测",
    );
    $("detection-dot").className =
      "dot" +
      (next.detecting
        ? " busy"
        : next.detection_error
          ? " error"
          : next.detected_at
            ? " success"
            : "");
    $("detection-label").textContent = t(
      next.waiting_for_proxy
        ? "等待代理就绪"
        : next.detecting
          ? "正在检测"
          : next.detection_error
            ? "检测未通过"
            : next.detected_at
              ? "公网连接正常"
              : "尚未验证",
    );
    $("detection-status").textContent = next.waiting_for_proxy
      ? t("正在等待公网 HTTPS 代理就绪，就绪后会自动验证…")
      : next.detecting
        ? t("正在验证公网地址，成功后将自动保存并生效…")
        : next.detection_error
          ? t(next.detection_error)
          : next.detected_at
            ? t("检测成功 · {proxy} · {date}", {
                proxy: next.detected_proxy
                  ? t("已信任代理 {proxy}", { proxy: next.detected_proxy })
                  : t("无需配置可信代理"),
                date: date(next.detected_at),
              })
            : next.public_url
              ? t("尚未验证，点击检测重试。")
              : t(
                  "请在部署环境中设置 RELAY_PUBLIC_URL，重新部署后会自动检测。",
                );
    $("registration-mode").textContent = t(
      next.registration_required
        ? "每个 token 只授权一台 Spool 服务器。"
        : "已开启自动注册；新服务器也可凭签名身份自动注册。",
    );
    $("public-url").textContent = next.public_url || t("尚未配置");
    $("web-domain").textContent = next.web_domain || t("尚未配置");
    if (fill && !settingsDirty) {
      $("trusted-proxies").value = (next.trusted_proxies || []).join(", ");
    }
  }
  const date = (value) => new Date(value).toLocaleString(locale());
  function node(tag, text, className) {
    const element = document.createElement(tag);
    element.textContent = text;
    if (className) element.className = className;
    return element;
  }
  function registrationGroup(entry) {
    if (entry.revoked_at || entry.unregistered_at) return "revoked";
    return entry.server_id ? "registered" : "unused";
  }
  function registrationState(entry) {
    return entry.revoked_at
      ? "已撤销"
      : entry.unregistered_at
        ? "已注销"
        : entry.server_id
          ? "已注册"
          : "未注册";
  }
  function webState(entry) {
    return entry.web_blocked
      ? "Web 已停用"
      : entry.web_url
        ? "Web 已发布"
        : "Web 未发布";
  }
  function hasTokenFilters() {
    return (
      $("token-search").value.trim() ||
      $("token-connection-filter").value !== "all" ||
      $("token-web-filter").value !== "all"
    );
  }
  function resetTokenFilters() {
    $("token-search").value = "";
    $("token-connection-filter").value = $("token-web-filter").value = "all";
  }
  async function loadTokens() {
    if (!credential) return;
    const epoch = generation;
    const revision = ++listRevision;
    const requestedDetail = detailsID;
    listBusy = true;
    listError = false;
    renderServers(registrations);
    try {
      const result = await api("/v1/registrations/search", {
        method: "POST",
        body: JSON.stringify({
          page: tokenPage,
          page_size: tokenPageSize,
          group: tokenFilter,
          query: $("token-search").value.trim(),
          connection: $("token-connection-filter").value,
          web: $("token-web-filter").value,
          selected: [...selectedTokens],
          detail_id: requestedDetail,
        }),
      });
      if (revision !== listRevision) return;
      tokenResult = result;
      tokenPage = result.page;
      const selected = new Map(
        result.selected.map((entry) => [entry.id, entry]),
      );
      selectedRecords.clear();
      for (const id of selectedTokens) {
        if (!selected.has(id)) {
          selectedTokens.delete(id);
          selectedRecords.delete(id);
        } else selectedRecords.set(id, selected.get(id));
      }
      for (const entry of result.registrations)
        selectedRecords.set(entry.id, entry);
      if (requestedDetail && requestedDetail === detailsID) {
        detailEntry = result.detail;
        if (!detailEntry) $("token-details").close();
      }
      registrations = result.registrations;
    } catch (error) {
      if (epoch === generation && revision !== listRevision) return;
      if (revision === listRevision) listError = true;
      throw error;
    } finally {
      if (revision === listRevision) {
        listBusy = false;
        renderServers(registrations);
      }
    }
  }
  function requestTokens() {
    loadTokens().catch((error) => {
      if (credential) showNotice(error.message);
    });
  }
  for (const [id, event] of [
    ["token-search", "input"],
    ["token-connection-filter", "change"],
    ["token-web-filter", "change"],
  ]) {
    $(id).addEventListener(event, () => {
      tokenPage = 1;
      selectedTokens.clear();
      selectedRecords.clear();
      requestTokens();
    });
  }
  $("clear-token-filters").addEventListener("click", () => {
    resetTokenFilters();
    tokenPage = 1;
    selectedTokens.clear();
    selectedRecords.clear();
    requestTokens();
  });
  function selectedEntries() {
    return [...selectedRecords.values()].filter((entry) =>
      selectedTokens.has(entry.id),
    );
  }
  function renderSelection() {
    const entries = selectedEntries();
    const eligible = pageTokens;
    const selectedOnPage = eligible.filter((entry) =>
      selectedTokens.has(entry.id),
    ).length;
    $("select-token-page").checked =
      eligible.length > 0 && selectedOnPage === eligible.length;
    $("select-token-page").indeterminate =
      selectedOnPage > 0 && selectedOnPage < eligible.length;
    $("select-token-page").disabled =
      bulkBusy || listBusy || eligible.length === 0;
    $("token-selection-count").textContent = t("已选 {count} 项", {
      count: entries.length,
    });
    $("token-bulk-actions").hidden = entries.length === 0;
    $("bulk-purge").disabled = $("clear-token-selection").disabled =
      bulkBusy || listBusy;
    $("bulk-revoke").disabled =
      bulkBusy ||
      listBusy ||
      !entries.some((entry) => registrationGroup(entry) !== "revoked");
    $("bulk-allow-web").disabled =
      bulkBusy ||
      listBusy ||
      !entries.some(
        (entry) =>
          registrationGroup(entry) !== "revoked" &&
          entry.server_id &&
          entry.web_blocked,
      );
    $("bulk-disable-web").disabled =
      bulkBusy ||
      listBusy ||
      !entries.some(
        (entry) =>
          registrationGroup(entry) !== "revoked" &&
          entry.server_id &&
          !entry.web_blocked,
      );
    for (const checkbox of $("servers").querySelectorAll(
      "[data-select-token]",
    )) {
      checkbox.checked = selectedTokens.has(checkbox.dataset.selectToken);
      checkbox.disabled =
        bulkBusy ||
        listBusy ||
        (selectedTokens.size >= 256 && !checkbox.checked);
    }
  }
  $("select-token-page").addEventListener("change", (event) => {
    for (const entry of pageTokens) {
      if (event.target.checked) {
        if (selectedTokens.size < 256) selectedTokens.add(entry.id);
      } else selectedTokens.delete(entry.id);
    }
    renderSelection();
  });
  $("clear-token-selection").addEventListener("click", () => {
    selectedTokens.clear();
    renderSelection();
  });
  function renderTokenDetails() {
    if (!detailsID) return;
    const entry = detailEntry;
    if (!entry) {
      $("token-details").close();
      return;
    }
    $("token-details-name").textContent = entry.name;
    $("detail-copy-status").textContent = t(detailCopyMessage);
    const values = {
      "detail-token-id": entry.id,
      "detail-device-id": entry.server_id || "—",
      "detail-device-status":
        t(registrationState(entry)) +
        (registrationGroup(entry) === "registered"
          ? " · " + t(entry.connected ? "在线" : "离线")
          : ""),
      "detail-registration-ip": entry.registration_ip || "—",
      "detail-web-status": t(webState(entry)),
      "detail-web-url": entry.web_url || "—",
      "detail-connections": String(entry.connections),
      "detail-created-at": date(entry.created_at),
      "detail-revoked-at": entry.revoked_at ? date(entry.revoked_at) : "—",
      "detail-unregistered-at": entry.unregistered_at
        ? date(entry.unregistered_at)
        : "—",
    };
    for (const [id, value] of Object.entries(values)) $(id).textContent = value;
    if (entry.web_url) $("detail-web-url").href = entry.web_url;
    else $("detail-web-url").removeAttribute("href");
    for (const button of document.querySelectorAll("[data-copy-detail]"))
      button.disabled = values[button.dataset.copyDetail] === "—";
  }
  function openTokenDetails(entry) {
    detailsID = entry.id;
    detailEntry = entry;
    detailCopyMessage = "";
    renderTokenDetails();
    $("token-details").showModal();
  }
  $("close-token-details").addEventListener("click", () =>
    $("token-details").close(),
  );
  $("token-details").addEventListener("click", (event) => {
    if (event.target !== $("token-details")) return;
    const rect = event.target.getBoundingClientRect();
    if (
      event.clientX < rect.left ||
      event.clientX > rect.right ||
      event.clientY < rect.top ||
      event.clientY > rect.bottom
    )
      event.target.close();
  });
  $("token-details").addEventListener("close", () => {
    if ($("token-details").open) return;
    const id = detailsID;
    detailsID = "";
    if (
      credential &&
      (document.activeElement === document.body ||
        $("token-details").contains(document.activeElement))
    ) {
      const row = [...$("servers").children].find(
        (row) => row.dataset.tokenId === id,
      );
      (row?.querySelector(".server-name") || $("token-panel")).focus({
        preventScroll: true,
      });
    }
  });
  for (const button of document.querySelectorAll("[data-copy-detail]"))
    button.addEventListener("click", async () => {
      const field = $(button.dataset.copyDetail);
      try {
        await navigator.clipboard.writeText(field.textContent);
        detailCopyMessage = "已复制。";
        $("detail-copy-status").textContent = t(detailCopyMessage);
      } catch {
        const range = document.createRange();
        range.selectNodeContents(field);
        getSelection().removeAllRanges();
        getSelection().addRange(range);
        detailCopyMessage = "请使用系统复制快捷键复制已选中的内容。";
        $("detail-copy-status").textContent = t(detailCopyMessage);
      }
    });
  function selectTokenFilter(filter) {
    tokenFilter = filter;
    tokenPage = 1;
    selectedTokens.clear();
    selectedRecords.clear();
    requestTokens();
  }
  function changeTokenPage(offset) {
    tokenPage += offset;
    requestTokens();
    $("token-panel").focus({ preventScroll: true });
    $("token-panel").scrollIntoView({ block: "start" });
  }
  $("token-previous-page").addEventListener("click", () => changeTokenPage(-1));
  $("token-next-page").addEventListener("click", () => changeTokenPage(1));
  function actionEntries(action) {
    return selectedEntries().filter(
      (entry) =>
        registrationGroup(entry) !== "revoked" &&
        (action === "revoke" ||
          (entry.server_id && entry.web_blocked === (action === "allow"))),
    );
  }
  function renderBulkDialog() {
    if (!bulkAction) return;
    const { action, entries } = bulkAction;
    const label =
      action === "revoke"
        ? "撤销 token"
        : action === "allow"
          ? "允许 Web"
          : "停用 Web";
    $("bulk-dialog-title").textContent = t(label);
    $("bulk-dialog-description").textContent = t(
      action === "revoke"
        ? "将撤销 {count} 个 token，并断开它们的 Relay 连接。此操作无法撤回。"
        : action === "allow"
          ? "将允许 {count} 台服务器发布 Web。服务器仍需自行开启 Web。"
          : "将停用 {count} 台服务器的 Web 访问。",
      { count: entries.length },
    );
    $("bulk-dialog-items").replaceChildren(
      ...entries.map((entry) => node("li", entry.name)),
    );
    $("confirm-bulk-action").textContent = t(label);
    $("confirm-bulk-action").className =
      action === "revoke" ? "danger" : "primary";
  }
  for (const [id, action] of [
    ["bulk-revoke", "revoke"],
    ["bulk-allow-web", "allow"],
    ["bulk-disable-web", "disable"],
  ]) {
    $(id).addEventListener("click", () => {
      if (bulkBusy) return;
      const entries = actionEntries(action);
      if (!entries.length) return;
      bulkAction = { action, entries };
      renderBulkDialog();
      $("bulk-dialog").returnValue = "cancel";
      $("bulk-dialog").showModal();
    });
  }
  $("bulk-dialog").addEventListener("cancel", () => {
    bulkAction = undefined;
  });
  $("bulk-dialog")
    .querySelector("form")
    .addEventListener("submit", (event) => {
      const operation = bulkAction;
      bulkAction = undefined;
      if (event.submitter?.value === "confirm" && operation && credential) {
        performTokenAction(
          operation.action,
          operation.entries.map((entry) => entry.id),
          true,
        );
      }
    });
  function renderPurgeDialog() {
    $("purge-description").textContent = t("将永久删除 {count} 条记录。", {
      count: purgeEntries.length,
    });
    $("purge-items").replaceChildren(
      ...purgeEntries.map((entry) => node("li", entry.name)),
    );
  }
  function openPurge(entries) {
    if (bulkBusy || listBusy || !entries.length) return;
    purgeEntries = entries;
    renderPurgeDialog();
    $("purge-confirmation").value = "";
    $("confirm-purge").disabled = true;
    $("purge-dialog").returnValue = "cancel";
    $("purge-dialog").showModal();
  }
  $("bulk-purge").addEventListener("click", () => openPurge(selectedEntries()));
  $("purge-confirmation").addEventListener("input", () => {
    $("confirm-purge").disabled = $("purge-confirmation").value !== "DELETE";
  });
  $("purge-dialog").addEventListener("cancel", () => {
    purgeEntries = [];
  });
  $("purge-dialog")
    .querySelector("form")
    .addEventListener("submit", async (event) => {
      const entries = purgeEntries;
      purgeEntries = [];
      if (
        event.submitter?.value !== "purge" ||
        $("purge-confirmation").value !== "DELETE" ||
        !credential ||
        bulkBusy
      )
        return;
      const epoch = generation;
      bulkBusy = true;
      renderServers(registrations);
      try {
        if (pending) await pending;
        if (epoch !== generation) return;
        const result = await api("/v1/registrations/purge", {
          method: "POST",
          body: JSON.stringify({ ids: entries.map((entry) => entry.id) }),
        });
        for (const entry of entries) {
          selectedTokens.delete(entry.id);
          selectedRecords.delete(entry.id);
        }
        issuedTokens = issuedTokens.filter(
          (result) =>
            !entries.some((entry) => entry.id === result.registration.id),
        );
        renderIssuedTokens();
        await refresh();
        if (epoch === generation)
          showNotice("已永久删除 {count} 条记录。", true, {
            count: result.purged,
          });
      } catch (error) {
        if (epoch === generation) showNotice(error.message);
      } finally {
        if (epoch === generation) {
          bulkBusy = false;
          renderServers(registrations);
        }
      }
    });
  const activityLabels = {
    generate: "生成 token",
    rotate: "重新签发 token",
    revoke: "撤销 token",
    allow: "允许 Web",
    disable: "停用 Web",
    purge: "永久删除",
    register: "设备注册",
    auto_registered: "自动注册",
    unregister: "设备注销",
  };
  function renderActivity() {
    const { events, page, pages, total } = activityResult;
    $("activity-rows").replaceChildren(
      ...events.map((event) => {
        const row = document.createElement("tr");
        const target = node("td", event.name || "—");
        if (event.target_id)
          target.append(node("span", event.target_id, "server-detail mono"));
        const cells = [
          node("td", date(event.time)),
          node("td", t(activityLabels[event.action] || event.action)),
          target,
          node(
            "td",
            t(event.result === "success" ? "成功" : "失败"),
            event.result === "success" ? "activity-success" : "activity-failed",
          ),
        ];
        ["操作时间", "操作", "目标", "结果"].forEach(
          (label, i) => (cells[i].dataset.label = t(label)),
        );
        row.append(...cells);
        return row;
      }),
    );
    $("activity-empty").hidden = total > 0;
    $("activity-table").hidden = total === 0;
    $("activity-pagination").hidden = total === 0;
    $("activity-page-status").textContent = t("第 {page} / {pages} 页", {
      page,
      pages,
    });
    $("activity-page-summary").textContent = t(
      "第 {start}–{end} 条，共 {total} 条",
      {
        start: total ? (page - 1) * 20 + 1 : 0,
        end: Math.min(page * 20, total),
        total,
      },
    );
    $("activity-previous-page").disabled = page === 1;
    $("activity-next-page").disabled = page === pages;
  }
  let activityRevision = 0;
  async function loadActivity() {
    const revision = ++activityRevision;
    $("activity-page").setAttribute("aria-busy", "true");
    $("activity-previous-page").disabled = $("activity-next-page").disabled =
      true;
    try {
      const result = await api("/v1/activity/search", {
        method: "POST",
        body: JSON.stringify({ page: activityPage, page_size: 20 }),
      });
      if (revision !== activityRevision) return;
      activityResult = result;
    } finally {
      if (revision === activityRevision) {
        // Failed navigation keeps the page whose events are still displayed.
        activityPage = activityResult.page;
        $("activity-page").setAttribute("aria-busy", "false");
        renderActivity();
      }
    }
  }
  for (const [id, offset] of [
    ["activity-previous-page", -1],
    ["activity-next-page", 1],
  ])
    $(id).addEventListener("click", () => {
      activityPage += offset;
      loadActivity().catch((error) => {
        if (credential) showNotice(error.message);
      });
    });
  async function performTokenAction(action, ids, batch = false) {
    if (bulkBusy || !credential) return;
    const epoch = generation;
    bulkBusy = true;
    renderServers(registrations);
    const completed = [];
    const failed = [];
    try {
      if (pending) await pending;
      for (const id of ids) {
        if (epoch !== generation) return;
        try {
          await api(
            "/v1/registrations/" + encodeURIComponent(id),
            action === "revoke"
              ? { method: "DELETE" }
              : {
                  method: "PATCH",
                  body: JSON.stringify({ web_blocked: action === "disable" }),
                },
          );
          completed.push(id);
          selectedTokens.delete(id);
        } catch (error) {
          if (epoch !== generation) return;
          failed.push(id);
          selectedTokens.add(id);
        }
      }
      if (action === "revoke") {
        issuedTokens = issuedTokens.filter(
          (result) => !completed.includes(result.registration.id),
        );
        renderIssuedTokens();
      }
      await refresh();
      if (epoch !== generation) return;
      if (failed.length) {
        showNotice(
          "成功 {success} 项，失败 {failed} 项。失败项保留选择，可重试。",
          false,
          { success: completed.length, failed: failed.length },
        );
      } else if (batch) {
        showNotice("已更新 {count} 项。", true, { count: completed.length });
      } else {
        showNotice(
          action === "revoke" ? "token 已撤销。" : "Web 发布资格已更新。",
          true,
        );
      }
    } catch (error) {
      if (epoch === generation) showNotice(error.message);
    } finally {
      if (epoch === generation) {
        bulkBusy = false;
        renderServers(registrations);
      }
    }
  }
  for (const [index, tab] of tokenTabs.entries()) {
    tab.addEventListener("click", () =>
      selectTokenFilter(tab.dataset.tokenFilter),
    );
    tab.addEventListener("keydown", (event) => {
      let next;
      if (event.key === "ArrowRight") next = (index + 1) % tokenTabs.length;
      else if (event.key === "ArrowLeft")
        next = (index - 1 + tokenTabs.length) % tokenTabs.length;
      else if (event.key === "Home") next = 0;
      else if (event.key === "End") next = tokenTabs.length - 1;
      else return;
      event.preventDefault();
      selectTokenFilter(tokenTabs[next].dataset.tokenFilter);
      tokenTabs[next].focus();
    });
  }
  function renderServers(list) {
    registrations = list;
    const counts = tokenResult.counts;
    for (const tab of tokenTabs) {
      const selected = tab.dataset.tokenFilter === tokenFilter;
      tab.setAttribute("aria-selected", String(selected));
      tab.tabIndex = selected ? 0 : -1;
      tab.disabled = bulkBusy;
      tab.querySelector(".tab-count").textContent =
        counts[tab.dataset.tokenFilter] || 0;
    }
    $("token-panel").setAttribute(
      "aria-labelledby",
      "token-tab-" + tokenFilter,
    );
    const { total, pages } = tokenResult;
    const start = (tokenPage - 1) * tokenPageSize;
    pageTokens = list;
    $("token-panel").setAttribute("aria-busy", String(listBusy));
    const rows = pageTokens.map((entry) => {
      const row = document.createElement("tr");
      row.dataset.tokenId = entry.id;
      const identity = document.createElement("td");
      const identityRow = node("div", "", "token-identity");
      {
        const checkbox = document.createElement("input");
        checkbox.type = "checkbox";
        checkbox.dataset.selectToken = entry.id;
        checkbox.setAttribute(
          "aria-label",
          t("选择 {name}", { name: entry.name }),
        );
        checkbox.addEventListener("change", () => {
          if (checkbox.checked && selectedTokens.size < 256)
            selectedTokens.add(entry.id);
          else selectedTokens.delete(entry.id);
          renderSelection();
        });
        identityRow.append(checkbox);
      }
      const identityCopy = node("div", "", "token-identity-copy");
      const name = node("button", entry.name, "server-name");
      name.type = "button";
      name.disabled = listBusy;
      name.setAttribute("aria-haspopup", "dialog");
      name.addEventListener("click", () => openTokenDetails(entry));
      identityCopy.append(name);
      identityCopy.append(
        node(
          "span",
          t("创建于 {date}", { date: date(entry.created_at) }) +
            (entry.automatic ? " · " + t("自动注册") : ""),
          "server-detail",
        ),
      );
      identityRow.append(identityCopy);
      identity.append(identityRow);
      const state = registrationState(entry);
      const status = node("td", "", "device-status");
      const badge = node(
        "span",
        t(state),
        "badge" +
          (registrationGroup(entry) === "revoked"
            ? " revoked"
            : registrationGroup(entry) === "unused"
              ? " unused"
              : entry.connected
                ? " online"
                : ""),
      );
      if (entry.server_id) badge.title = entry.server_id;
      status.append(badge);
      if (registrationGroup(entry) === "registered")
        status.append(
          node(
            "span",
            t(entry.connected ? "在线" : "离线"),
            "connection-state",
          ),
        );
      const action = document.createElement("td");
      const actions = node("div", "", "server-actions");
      action.append(actions);
      if (!entry.revoked_at && !entry.unregistered_at) {
        if (entry.server_id) {
          const webButton = node(
            "button",
            t(entry.web_blocked ? "允许 Web" : "停用 Web"),
            "quiet",
          );
          webButton.disabled = bulkBusy || listBusy;
          webButton.addEventListener("click", () =>
            performTokenAction(entry.web_blocked ? "allow" : "disable", [
              entry.id,
            ]),
          );
          actions.append(webButton);
        }
        const button = node("button", t("撤销"), "quiet revoke");
        button.disabled = bulkBusy || listBusy;
        button.addEventListener("click", () => revoke(entry));
        actions.append(button);
      }
      const purge = node("button", t("永久删除"), "quiet revoke purge");
      purge.disabled = bulkBusy || listBusy;
      purge.addEventListener("click", () => openPurge([entry]));
      actions.append(purge);
      const ip = node("td", entry.registration_ip || "—", "mono");
      const web = document.createElement("td");
      const webStatus = node("div", "", "web-status");
      if (entry.web_url) {
        const link = node("a", "", "web-link");
        link.href = entry.web_url;
        link.title = entry.web_url;
        link.setAttribute("aria-label", entry.web_url);
        link.target = "_blank";
        link.rel = "noopener noreferrer";
        const svg = document.createElementNS(
          "http://www.w3.org/2000/svg",
          "svg",
        );
        svg.setAttribute("viewBox", "0 0 24 24");
        svg.setAttribute("aria-hidden", "true");
        const circle = document.createElementNS(svg.namespaceURI, "circle");
        circle.setAttribute("cx", "12");
        circle.setAttribute("cy", "12");
        circle.setAttribute("r", "10");
        const path = document.createElementNS(svg.namespaceURI, "path");
        path.setAttribute(
          "d",
          "M2 12h20M12 2a18 18 0 0 1 0 20 18 18 0 0 1 0-20",
        );
        svg.append(circle, path);
        link.append(svg);
        webStatus.append(link);
      } else {
        const unavailable = node("span", "—", "muted");
        unavailable.title = t("Web 未发布");
        webStatus.append(unavailable);
      }
      const connections = node(
        "span",
        String(entry.connections),
        "connection-count",
      );
      connections.title = t("当前连接");
      webStatus.append(connections);
      web.append(webStatus);
      status.dataset.label = t("设备状态");
      ip.dataset.label = t("注册 IP");
      web.dataset.label = t("Web 状态");
      action.dataset.label = t("操作");
      row.append(identity, status, ip, web, action);
      return row;
    });
    $("servers").replaceChildren(...rows);
    $("empty-state").textContent = t(
      hasTokenFilters()
        ? "没有匹配的 token，请调整搜索或筛选条件。"
        : emptyStates[tokenFilter],
    );
    $("token-list-status").textContent = t(
      listBusy
        ? "正在加载…"
        : listError
          ? "列表加载失败，请点击刷新重试。"
          : "",
    );
    $("token-list-status").hidden = !listBusy && !listError;
    $("empty-state").hidden = total > 0 || listError || listBusy;
    $("token-table").hidden = total === 0 || listError;
    $("token-pagination").hidden = total === 0 || listError;
    $("token-page-summary").textContent = t(
      "第 {start}–{end} 条，共 {total} 条",
      {
        start: total ? start + 1 : 0,
        end: Math.min(start + tokenPageSize, total),
        total: total,
      },
    );
    $("token-page-status").textContent = t("第 {page} / {pages} 页", {
      page: tokenPage,
      pages,
    });
    $("token-previous-page").disabled = bulkBusy || listBusy || tokenPage === 1;
    $("token-next-page").disabled = bulkBusy || listBusy || tokenPage === pages;
    for (const id of [
      "token-search",
      "token-connection-filter",
      "token-web-filter",
    ])
      $(id).disabled = bulkBusy;
    $("clear-token-filters").disabled = bulkBusy || !hasTokenFilters();
    $("refresh").disabled = bulkBusy || !!pending;
    renderSelection();
    renderTokenDetails();
    $("online-count").textContent = tokenResult.metrics.online || 0;
    $("active-count").textContent = tokenResult.metrics.active || 0;
    $("connection-count").textContent = tokenResult.metrics.connections || 0;
  }
  function refresh() {
    if (!credential) return Promise.resolve();
    if (pending) return pending;
    const request = Promise.all([
      api("/v1/settings"),
      loadTokens(),
      currentPage === "activity" ? loadActivity() : null,
    ])
      .then(([next]) => {
        renderSettings(next, true);
      })
      .finally(() => {
        if (pending === request) {
          pending = undefined;
          $("refresh").disabled = bulkBusy;
          $("refresh").removeAttribute("aria-busy");
        }
      });
    pending = request;
    $("refresh").disabled = true;
    $("refresh").setAttribute("aria-busy", "true");
    return request;
  }
  function navigate(page) {
    currentPage = page;
    const network = page === "network";
    $("activity-page").hidden = page !== "activity";
    $("network-page").hidden = !network;
    $("servers-page").hidden = page !== "servers";
    $("page-title").textContent = t(
      page === "activity" ? "操作记录" : network ? "公网连接" : "服务器授权",
    );
    $("page-description").textContent = t(
      page === "activity"
        ? "查看 token 和 Web 权限的变更记录。"
        : network
          ? "查看 Relay 的公网入口与代理连接状态。"
          : "管理 token 的生成、注册状态与访问权限。",
    );
    for (const button of document.querySelectorAll("[data-page]")) {
      const selected = button.dataset.page === page;
      button.classList.toggle("selected", selected);
      if (selected) button.setAttribute("aria-current", "page");
      else button.removeAttribute("aria-current");
    }
    $("settings-main").scrollTo(0, 0);
  }
  for (const button of document.querySelectorAll("[data-page]")) {
    button.addEventListener("click", () => {
      navigate(button.dataset.page);
      if (button.dataset.page === "activity")
        loadActivity().catch((error) => showNotice(error.message));
      setSidebar(false, true);
    });
  }
  function poll() {
    clearTimeout(timer);
    if (!credential) return;
    timer = setTimeout(
      async () => {
        try {
          if (!bulkBusy) await refresh();
        } catch (error) {
          showNotice(error.message);
        }
        poll();
      },
      settings?.detecting ? 800 : 10000,
    );
  }
  async function saveSettings() {
    if (savingSettings) return;
    savingSettings = true;
    $("trusted-proxies").disabled = $("save-settings").disabled = true;
    $("detect").disabled = true;
    try {
      const next = await api("/v1/settings", {
        method: "PUT",
        body: JSON.stringify({
          browser_origin: location.origin,
          trusted_proxies: $("trusted-proxies")
            .value.split(",")
            .map((v) => v.trim())
            .filter(Boolean),
        }),
      });
      settingsDirty = false;
      renderSettings(next, true);
      poll();
    } finally {
      savingSettings = false;
      $("trusted-proxies").disabled = $("save-settings").disabled = false;
      if (settings) renderSettings(settings);
    }
  }
  $("login-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    credential = $("admin-token").value.trim();
    $("admin-token").value = "";
    $("login-button").disabled = true;
    showNotice("");
    try {
      settingsDirty = false;
      await refresh();
      $("login").hidden = true;
      $("dashboard").hidden = false;
      $("logout").hidden = false;
      // Remember the management origin for tenant isolation. Its listener can
      // differ from the public Relay entry, so it never supplies the probe URL.
      if (
        location.protocol === "https:" &&
        settings.admin_origin !== location.origin
      )
        await saveSettings();
      poll();
    } catch (error) {
      showNotice(error.message);
    } finally {
      $("login-button").disabled = location.protocol !== "https:" && !local;
    }
  });
  $("logout").addEventListener("click", () => {
    logout();
    showNotice("");
  });
  $("refresh").addEventListener("click", async () => {
    try {
      await refresh();
      showNotice("");
    } catch (error) {
      showNotice(error.message);
    }
  });
  $("settings-form").addEventListener("input", () => {
    settingsDirty = true;
  });
  $("settings-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    try {
      await saveSettings();
      showNotice("设置已保存。", true);
    } catch (error) {
      showNotice(error.message);
    }
  });
  $("detect").addEventListener("click", async () => {
    $("detect").disabled = true;
    try {
      renderSettings(await api("/v1/proxy-detection", { method: "POST" }));
      showNotice("");
      poll();
    } catch (error) {
      showNotice(error.message);
      if (settings) renderSettings(settings);
    }
  });
  $("create-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    if (creating) return;
    const batch = tokenMode === "batch";
    const count = Number($("batch-count").value);
    if (batch && (!Number.isInteger(count) || count < 1 || count > 100)) {
      showNotice("count must be an integer between 1 and 100");
      return;
    }
    creating = true;
    $("create-token").disabled = true;
    renderTokenMode();
    try {
      const result = await api(
        batch ? "/v1/registrations/batch" : "/v1/registrations",
        {
          method: "POST",
          body: JSON.stringify(
            batch ? { count } : { name: $("server-name").value.trim() },
          ),
        },
      );
      issuedTokens.push(...(batch ? result.registrations : [result]));
      batchResult = batch;
      renderIssuedTokens();
      $("server-name").value = "";
      showNotice("");
      resetTokenFilters();
      selectTokenFilter("unused");
      await refresh();
    } catch (error) {
      showNotice(error.message);
    } finally {
      creating = false;
      $("create-token").disabled = false;
      renderTokenMode();
    }
  });
  $("dismiss-token").addEventListener("click", () => {
    issuedTokens = [];
    batchResult = false;
    renderIssuedTokens();
  });
  function renderTokenMode() {
    const batch = tokenMode === "batch";
    $("server-name").hidden = $("single-name-label").hidden = batch;
    $("batch-count").hidden = $("batch-count-label").hidden = !batch;
    $("batch-count").disabled = !batch || creating;
    $("server-name").disabled = batch || creating;
    $("create-token").textContent = t(
      creating ? "生成中…" : batch ? "批量生成 token" : "生成 token",
    );
    for (const button of document.querySelectorAll("[data-token-mode]")) {
      button.setAttribute(
        "aria-pressed",
        String(button.dataset.tokenMode === tokenMode),
      );
      button.disabled = creating;
    }
  }
  for (const button of document.querySelectorAll("[data-token-mode]"))
    button.addEventListener("click", () => {
      tokenMode = button.dataset.tokenMode;
      renderTokenMode();
    });
  function renderIssuedTokens() {
    $("token-result").hidden = issuedTokens.length === 0;
    $("token-result-title").textContent = t("本页生成：{count} 个 token", {
      count: issuedTokens.length,
    });
    const summary = batchResult;
    $("token-result-hint").textContent = t(
      summary
        ? "请导出 TXT 查看名称和 token。关闭页面后无法再次导出。"
        : "请及时复制或导出。本次生成的 token 仅保留在当前页面，关闭后无法再次查看。",
    );
    $("issued-results").replaceChildren(
      ...(summary ? [] : issuedTokens.slice(-1)).map((result, index) => {
        const row = node("div", "", "issued-row");
        const name = node(
          "label",
          result.registration.name,
          "issued-name mono",
        );
        const input = document.createElement("input");
        input.id = index === 0 ? "issued-token" : "issued-token-" + index;
        name.htmlFor = input.id;
        input.readOnly = true;
        input.spellcheck = false;
        input.value = result.token;
        const copy = node("button", t("复制"), "quiet");
        copy.type = "button";
        copy.addEventListener("click", async () => {
          try {
            await navigator.clipboard.writeText(input.value);
            showNotice("token 已复制。", true);
          } catch {
            input.focus();
            input.select();
            showNotice("请使用系统复制快捷键复制已选中的 token。");
          }
        });
        const control = node("div", "", "copy-row");
        control.append(input, copy);
        row.append(name, control);
        return row;
      }),
    );
  }
  $("export-tokens").addEventListener("click", () => {
    if (!issuedTokens.length) return;
    const content =
      issuedTokens
        .map(({ registration, token }) => registration.name + "\t" + token)
        .join("\r\n") + "\r\n";
    const url = URL.createObjectURL(
      new Blob([content], { type: "text/plain;charset=utf-8" }),
    );
    const link = document.createElement("a");
    link.href = url;
    link.download = "spool-relay-tokens.txt";
    document.body.append(link);
    link.click();
    link.remove();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  });
  function renderRevokeDialog() {
    if (revokeEntry)
      $("revoke-description").textContent = t("即将撤销 token「{name}」。", {
        name: revokeEntry.name,
      });
  }
  function revoke(entry) {
    const dialog = $("revoke-dialog");
    revokeEntry = entry;
    renderRevokeDialog();
    dialog.returnValue = "cancel";
    dialog.showModal();
  }
  $("revoke-dialog").addEventListener("cancel", () => {
    revokeEntry = undefined;
  });
  $("revoke-dialog")
    .querySelector("form")
    .addEventListener("submit", (event) => {
      const entry = revokeEntry;
      revokeEntry = undefined;
      if (event.submitter?.value === "revoke" && entry && credential)
        performTokenAction("revoke", [entry.id]);
    });
  document.addEventListener("relay-presentation-change", () => {
    if (settings) renderSettings(settings);
    if (credential) renderServers(registrations);
    renderBulkDialog();
    renderRevokeDialog();
    if (purgeEntries.length) renderPurgeDialog();
    if (notice) showNotice(notice.message, notice.success, notice.values);
    navigate(currentPage);
    renderActivity();
    renderIssuedTokens();
    renderTokenMode();
  });
  navigate("servers");
  renderServers([]);
  renderTokenMode();
})();
