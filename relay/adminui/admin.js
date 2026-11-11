"use strict";
(() => {
  const $ = (id) => document.getElementById(id);
  let credential = "";
  let settings;
  let timer;
  let generation = 0;
  let pending = false;
  let settingsDirty = false;
  let savingSettings = false;

  const local = location.hostname === "localhost" || location.hostname === "[::1]" || /^127\.\d+\.\d+\.\d+$/.test(location.hostname);
  if (location.protocol !== "https:" && !local) {
    $("login-button").disabled = true;
    showNotice("请使用 HTTPS 管理网址，或通过 SSH 将管理端口转发到本机后访问。管理员 token 需要安全的连接。");
  }

  function showNotice(message, success = false) {
    $("notice").textContent = message;
    $("notice").hidden = !message;
    $("notice").className = success ? "success" : "";
  }
  function logout() {
    generation++;
    credential = "";
    settings = undefined;
    clearTimeout(timer);
    $("admin-token").value = "";
    $("issued-token").value = "";
    $("token-result").hidden = true;
    $("servers").replaceChildren();
    $("dashboard").hidden = true;
    $("logout").hidden = true;
    $("login").hidden = false;
    $("revoke-dialog").close();
    pending = false;
  }
  async function api(path, options = {}) {
    const epoch = generation;
    const response = await fetch(path, {
      ...options,
      credentials: "omit",
      cache: "no-store",
      redirect: "error",
      headers: { Authorization: "Bearer " + credential, ...(options.body ? { "Content-Type": "application/json" } : {}), ...options.headers },
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
    $("public-address").textContent = next.public_url || "尚未设置公网地址";
    $("detected-proxy").textContent = next.detected_proxy || (next.detected_at ? "直接 TLS 连接，无需代理 IP" : "尚无");
    $("detect").disabled = !next.public_url || next.detecting || savingSettings;
    $("detect").textContent = next.waiting_for_proxy ? "等待代理…" : next.detecting ? "检测中…" : "检测";
    $("detection-dot").className = "dot" + (next.detecting ? " busy" : next.detected_at && !next.detection_error ? " success" : "");
    $("detection-status").textContent = next.waiting_for_proxy ? "正在等待公网 HTTPS 代理就绪，就绪后会自动验证…" : next.detecting ? "正在验证公网地址，成功后将自动保存并生效…" : next.detection_error || (next.detected_at ? "检测成功 · " + (next.detected_proxy ? "已信任代理 " + next.detected_proxy : "无需配置可信代理") + " · " + new Date(next.detected_at).toLocaleString() : next.public_url ? "尚未验证，点击检测重试。" : "请在部署环境中设置 RELAY_PUBLIC_URL，重新部署后会自动检测。");
    $("registration-mode").textContent = next.registration_required ? "每个 token 只授权一台 Spool 服务器。" : "已开启自动注册；新服务器也可凭签名身份自动注册。";
    $("public-url").value = next.public_url;
    $("web-domain").value = next.web_domain || "";
    if (fill && !settingsDirty) {
      $("trusted-proxies").value = (next.trusted_proxies || []).join(", ");
    }
  }
  const date = (value) => new Date(value).toLocaleString();
  function node(tag, text, className) {
    const element = document.createElement(tag);
    element.textContent = text;
    if (className) element.className = className;
    return element;
  }
  function renderServers(list) {
    const rows = list.slice().reverse().map((entry) => {
      const row = document.createElement("tr");
      const identity = document.createElement("td");
      identity.append(node("div", entry.name, "server-name"));
      identity.append(node("span", entry.server_id || "等待服务器首次注册", "server-detail mono"));
      if (entry.web_url) {
        const link = node("a", entry.web_url, "server-detail mono");
        link.href = entry.web_url; link.target = "_blank"; link.rel = "noopener noreferrer";
        identity.append(link);
      } else if (entry.server_id && !entry.revoked_at && !entry.unregistered_at) identity.append(node("span", "Web 未发布", "server-detail"));
      identity.append(node("span", "创建于 " + date(entry.created_at) + (entry.automatic ? " · 自动注册" : ""), "server-detail"));
      const state = entry.revoked_at ? "已撤销" : entry.unregistered_at ? "已注销" : entry.connected ? "在线" : entry.server_id ? "离线" : "待注册";
      const status = document.createElement("td");
      status.append(node("span", state, "badge" + (entry.revoked_at ? " revoked" : entry.connected ? " online" : "")));
      const action = document.createElement("td");
      if (!entry.revoked_at && !entry.unregistered_at) {
        if (entry.server_id) {
          const webButton = node("button", entry.web_blocked ? "允许 Web" : "停用 Web", "quiet");
          webButton.addEventListener("click", async () => {
            webButton.disabled = true;
            try { await api("/v1/registrations/" + encodeURIComponent(entry.id), { method: "PATCH", body: JSON.stringify({web_blocked: !entry.web_blocked}) }); await refresh(); showNotice("Web 发布资格已更新。", true); }
            catch(error) { showNotice(error.message); webButton.disabled = false; }
          });
          action.append(webButton);
        }
        const button = node("button", "撤销", "quiet revoke");
        button.addEventListener("click", () => revoke(entry));
        action.append(button);
      } else action.textContent = "—";
      const ip = node("td", entry.registration_ip || "—", "mono");
      const connections = node("td", String(entry.connections));
      status.dataset.label = "状态";
      ip.dataset.label = "注册 IP";
      connections.dataset.label = "当前连接";
      action.dataset.label = "操作";
      row.append(identity, status, ip, connections, action);
      return row;
    });
    $("servers").replaceChildren(...rows);
    $("empty-state").hidden = list.length > 0;
    $("online-count").textContent = list.filter((e) => e.connected).length;
    $("active-count").textContent = list.filter((e) => !e.revoked_at && !e.unregistered_at).length;
    $("connection-count").textContent = list.reduce((total, e) => total + e.connections, 0);
  }
  async function refresh() {
    if (!credential || pending) return;
    pending = true;
    try {
      const [next, list] = await Promise.all([api("/v1/settings"), api("/v1/registrations")]);
      renderSettings(next, true);
      renderServers(list.registrations);
    } finally {
      pending = false;
    }
  }
  function poll() {
    clearTimeout(timer);
    if (!credential) return;
    timer = setTimeout(async () => {
      try { await refresh(); } catch (error) { showNotice(error.message); }
      poll();
    }, settings?.detecting ? 800 : 10000);
  }
  async function saveSettings() {
    if (savingSettings) return;
    savingSettings = true;
    $("trusted-proxies").disabled = $("save-settings").disabled = true;
    $("detect").disabled = true;
    try {
      const next = await api("/v1/settings", { method: "PUT", body: JSON.stringify({
        browser_origin: location.origin,
        trusted_proxies: $("trusted-proxies").value.split(",").map((v) => v.trim()).filter(Boolean),
      }) });
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
      if (location.protocol === "https:" && settings.admin_origin !== location.origin) await saveSettings();
      poll();
    } catch (error) { showNotice(error.message); }
    finally { $("login-button").disabled = location.protocol !== "https:" && !local; }
  });
  $("logout").addEventListener("click", () => { logout(); showNotice(""); });
  $("refresh").addEventListener("click", async () => { try { await refresh(); showNotice(""); } catch (error) { showNotice(error.message); } });
  $("settings-form").addEventListener("input", () => { settingsDirty = true; });
  $("settings-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    try { await saveSettings(); showNotice("设置已保存。", true); } catch (error) { showNotice(error.message); }
  });
  $("detect").addEventListener("click", async () => {
    $("detect").disabled = true;
    try { renderSettings(await api("/v1/proxy-detection", { method: "POST" })); showNotice(""); poll(); }
    catch (error) { showNotice(error.message); if (settings) renderSettings(settings); }
  });
  $("create-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    $("create-token").disabled = true;
    try {
      const result = await api("/v1/registrations", { method: "POST", body: JSON.stringify({ name: $("server-name").value.trim() }) });
      $("issued-token").value = result.token;
      $("token-result").hidden = false;
      $("server-name").value = "";
      showNotice("");
      await refresh();
    } catch (error) { showNotice(error.message); }
    finally { $("create-token").disabled = false; }
  });
  $("dismiss-token").addEventListener("click", () => { $("issued-token").value = ""; $("token-result").hidden = true; });
  $("copy-token").addEventListener("click", async () => {
    try {
      await navigator.clipboard.writeText($("issued-token").value);
      showNotice("token 已复制。", true);
    } catch {
      $("issued-token").focus(); $("issued-token").select();
      showNotice("请使用系统复制快捷键复制已选中的 token。");
    }
  });
  async function revoke(entry) {
    const dialog = $("revoke-dialog");
    $("revoke-description").textContent = "即将撤销「" + entry.name + "」的授权。";
    dialog.returnValue = "cancel";
    dialog.showModal();
    dialog.addEventListener("close", async () => {
      if (dialog.returnValue !== "revoke" || !credential) return;
      try {
        await api("/v1/registrations/" + encodeURIComponent(entry.id), { method: "DELETE" });
        $("issued-token").value = ""; $("token-result").hidden = true;
        await refresh(); showNotice("授权已撤销。", true);
      } catch (error) { showNotice(error.message); }
    }, { once: true });
  }
})();
