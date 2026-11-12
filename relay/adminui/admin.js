import { locale, t } from "./presentation.js";
(() => {
  const $ = (id) => document.getElementById(id);
  let credential = "";
  let settings;
  let timer;
  let generation = 0;
  let pending = false;
  let settingsDirty = false;
  let savingSettings = false;
  let issuedTokens = [];
  let batchResult = false;
  let tokenMode = "single";
  let creating = false;
  let registrations = [];
  let tokenFilter = "registered";
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
    renderServers([]);
    $("server-name").value = "";
    $("dashboard").hidden = true;
    $("logout").hidden = true;
    $("login").hidden = false;
    navigate("servers");
    $("revoke-dialog").close();
    $("appearance-dialog").close();
    pending = false;
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
  function selectTokenFilter(filter) {
    tokenFilter = filter;
    renderServers(registrations);
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
    const counts = { registered: 0, revoked: 0, unused: 0, all: list.length };
    for (const entry of list) counts[registrationGroup(entry)]++;
    for (const tab of tokenTabs) {
      const selected = tab.dataset.tokenFilter === tokenFilter;
      tab.setAttribute("aria-selected", String(selected));
      tab.tabIndex = selected ? 0 : -1;
      tab.querySelector(".tab-count").textContent =
        counts[tab.dataset.tokenFilter];
    }
    $("token-panel").setAttribute(
      "aria-labelledby",
      "token-tab-" + tokenFilter,
    );
    const visible = list.filter(
      (entry) =>
        tokenFilter === "all" || registrationGroup(entry) === tokenFilter,
    );
    const rows = visible
      .slice()
      .reverse()
      .map((entry) => {
        const row = document.createElement("tr");
        const identity = document.createElement("td");
        identity.append(node("div", entry.name, "server-name"));
        identity.append(
          node(
            "span",
            entry.server_id ||
              t(
                registrationGroup(entry) === "revoked"
                  ? "未绑定服务器"
                  : "等待服务器首次注册",
              ),
            "server-detail mono",
          ),
        );
        if (entry.web_url) {
          const link = node("a", entry.web_url, "server-detail mono");
          link.href = entry.web_url;
          link.target = "_blank";
          link.rel = "noopener noreferrer";
          identity.append(link);
        } else if (
          entry.server_id &&
          !entry.revoked_at &&
          !entry.unregistered_at
        )
          identity.append(node("span", t("Web 未发布"), "server-detail"));
        identity.append(
          node(
            "span",
            t("创建于 {date}", { date: date(entry.created_at) }) +
              (entry.automatic ? " · " + t("自动注册") : ""),
            "server-detail",
          ),
        );
        const state = entry.revoked_at
          ? "已撤销"
          : entry.unregistered_at
            ? "已注销"
            : entry.server_id
              ? "已注册"
              : "未注册";
        const status = document.createElement("td");
        status.append(
          node(
            "span",
            t(state),
            "badge" +
              (registrationGroup(entry) === "revoked"
                ? " revoked"
                : entry.connected
                  ? " online"
                  : ""),
          ),
        );
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
            webButton.addEventListener("click", async () => {
              webButton.disabled = true;
              try {
                await api("/v1/registrations/" + encodeURIComponent(entry.id), {
                  method: "PATCH",
                  body: JSON.stringify({ web_blocked: !entry.web_blocked }),
                });
                await refresh();
                showNotice("Web 发布资格已更新。", true);
              } catch (error) {
                showNotice(error.message);
                webButton.disabled = false;
              }
            });
            actions.append(webButton);
          }
          const button = node("button", t("撤销"), "quiet revoke");
          button.addEventListener("click", () => revoke(entry));
          actions.append(button);
        } else action.textContent = "—";
        const ip = node("td", entry.registration_ip || "—", "mono");
        const connections = node("td", String(entry.connections));
        status.dataset.label = t("Token 状态");
        ip.dataset.label = t("注册 IP");
        connections.dataset.label = t("当前连接");
        action.dataset.label = t("操作");
        row.append(identity, status, ip, connections, action);
        return row;
      });
    $("servers").replaceChildren(...rows);
    $("empty-state").textContent = t(emptyStates[tokenFilter]);
    $("empty-state").hidden = visible.length > 0;
    $("token-table").hidden = visible.length === 0;
    $("online-count").textContent = list.filter((e) => e.connected).length;
    $("active-count").textContent = list.filter(
      (e) => !e.revoked_at && !e.unregistered_at,
    ).length;
    $("connection-count").textContent = list.reduce(
      (total, e) => total + e.connections,
      0,
    );
  }
  async function refresh() {
    if (!credential || pending) return;
    pending = true;
    try {
      const [next, list] = await Promise.all([
        api("/v1/settings"),
        api("/v1/registrations"),
      ]);
      renderSettings(next, true);
      renderServers(list.registrations);
    } finally {
      pending = false;
    }
  }
  function navigate(page) {
    const network = page === "network";
    $("network-page").hidden = !network;
    $("servers-page").hidden = network;
    $("page-title").textContent = t(network ? "公网连接" : "服务器授权");
    $("page-description").textContent = t(
      network
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
      setSidebar(false, true);
    });
  }
  function poll() {
    clearTimeout(timer);
    if (!credential) return;
    timer = setTimeout(
      async () => {
        try {
          await refresh();
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
  async function revoke(entry) {
    const dialog = $("revoke-dialog");
    const description = () => {
      $("revoke-description").textContent = t("即将撤销 token「{name}」。", {
        name: entry.name,
      });
    };
    description();
    document.addEventListener("relay-presentation-change", description);
    dialog.returnValue = "cancel";
    dialog.showModal();
    dialog.addEventListener(
      "close",
      async () => {
        document.removeEventListener("relay-presentation-change", description);
        if (dialog.returnValue !== "revoke" || !credential) return;
        try {
          await api("/v1/registrations/" + encodeURIComponent(entry.id), {
            method: "DELETE",
          });
          issuedTokens = issuedTokens.filter(
            (result) => result.registration.id !== entry.id,
          );
          renderIssuedTokens();
          await refresh();
          showNotice("token 已撤销。", true);
        } catch (error) {
          showNotice(error.message);
        }
      },
      { once: true },
    );
  }
  document.addEventListener("relay-presentation-change", () => {
    if (settings) renderSettings(settings);
    if (credential) renderServers(registrations);
    if (notice) showNotice(notice.message, notice.success, notice.values);
    navigate($("network-page").hidden ? "servers" : "network");
    renderIssuedTokens();
    renderTokenMode();
  });
  navigate("servers");
  renderServers([]);
  renderTokenMode();
})();
