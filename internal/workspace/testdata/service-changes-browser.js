(async () => {
 const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
 const waitFor = async (check, label) => {
  for (let i = 0; i < 100; i++) { if (check()) return; await pause(30); }
  throw new Error("Timed out: " + label);
 };
 const fixture = window.smokeSettings;
 const pending = () => [...document.querySelectorAll('[data-service-change]')];
 const idle = () => !document.querySelector('.settings-page-controls[aria-busy="true"]');
 const refresh = () => fixture.sectionListeners.forEach(listener => listener("Service"));
 const field = label => document.querySelector(`[aria-label="${label}"]`);
 const set = (input, value) => {
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value").set.call(input, value);
  input.dispatchEvent(new Event("input", {bubbles: true}));
 };
 await waitFor(() => document.querySelector('[aria-label="HTTP 端口"]'), "service settings");
 const original = structuredClone(fixture.snapshot.settings);
 await mygo.call("DesktopSettings.Execute", { kind: "save-settings", settings: {
  ...original, port: 29876, tsnet_hostname: "pending-node", relay_enabled: true, relay_web_enabled: true, relay_url: "https://relay.example.test", relay_lan_port: 28082,
 }});
 refresh();
 await waitFor(() => pending().length === 3, "three pending entries");
 const bar = document.querySelector('.settings-restart-bar');
 if (!bar.textContent.includes("Web 入口") || !bar.textContent.includes("Spool 中继") || !bar.textContent.includes("Tailscale") || !bar.textContent.includes("正在执行的任务继续运行")) throw new Error("Missing entry-specific restart descriptions");
 if (document.querySelectorAll('[data-apply-service-change]').length !== 3 || document.querySelectorAll('[data-cancel-service-change]').length !== 3) throw new Error("Missing independent actions");
 const bounds = bar.getBoundingClientRect();
 if (bounds.top < 0 || bounds.bottom > innerHeight || innerHeight - bounds.bottom > 24 || bounds.left < 0 || bounds.right > innerWidth) throw new Error("Bottom bar escaped the viewport");
 document.querySelector('.settings-main').scrollTop = 100000;
 await pause(40);
 if (Math.abs(bar.getBoundingClientRect().top - bounds.top) > 1) throw new Error("Bottom bar moved while scrolling");
 document.querySelector('.settings-main').scrollTop = 0;
 const capture = await fetch('/capture', {method: 'POST'});
 if (!capture.ok) throw new Error("Capture failed");
 // Incomplete inputs never reach the backend, but Cancel must discard them.
 set(field("HTTP 端口"), "");
 set(field("Tailscale 节点名称"), "unfinished-node");
 set(field("代理 URL"), "http://unfinished-proxy");
 await pause(40);
 fixture.failNextCommand = "cancel-service-changes";
 document.querySelector('[data-cancel-service-change="web"]').click();
 await waitFor(() => fixture.commands.some(command => command.kind === "cancel-service-changes") && idle(), "failed Web cancel");
 if (pending().length !== 3 || field("HTTP 端口").value !== "" || fixture.snapshot.settings.port !== 29876) throw new Error("Failed cancel discarded the draft or saved settings");
 document.querySelector('[data-cancel-service-change="web"]').click();
 await waitFor(() => pending().length === 2 && idle(), "cancel only Web");
 if (fixture.snapshot.settings.port !== original.port || fixture.snapshot.settings.tsnet_hostname !== "pending-node") throw new Error("Cancel overwrote unrelated pending settings");
 if (document.querySelector('[aria-label="HTTP 端口"]').value !== String(original.port)) throw new Error("Cancelled field did not restore visually");
 if (field("Tailscale 节点名称").value !== "unfinished-node" || field("代理 URL").value !== "http://unfinished-proxy") throw new Error("Web cancel discarded unrelated drafts");
 document.querySelector('[data-apply-service-change="relay"]').click();
 await waitFor(() => pending().length === 1 && pending()[0].dataset.serviceChange === 'tsnet' && idle(), "apply only relay");
 document.querySelector('[data-cancel-service-change="tsnet"]').click();
 await waitFor(() => !document.querySelector('.settings-restart-bar') && idle(), "cancel last pending entry");
 if (fixture.snapshot.settings.tsnet_hostname !== original.tsnet_hostname || fixture.commands.some(command => command.kind === "restart-service")) throw new Error("Apply restarted core or cancel failed");
 if (field("Tailscale 节点名称").value !== original.tsnet_hostname || field("代理 URL").value !== original.proxy_url) throw new Error("Tailscale cancel did not restore service and network drafts");
 // Web scope is a switch, and the Web listener can be disabled independently.
 const web = () => field("启用 Web 入口");
 const lan = () => field("允许局域网访问");
 const webPending = () => document.querySelector('[data-service-change="web"]');
 const webAction = async action => {
  document.querySelector(`[data-${action}-service-change="web"]`).click();
  await waitFor(() => !webPending() && idle(), `${action} Web switch`);
 };
 if (document.querySelector('input[aria-label^="监听 IP"]') || !field("公网地址")) throw new Error("Web settings still expose listen IP or the old URL label");
 lan().click();
 await waitFor(() => fixture.snapshot.settings.host === "0.0.0.0" && webPending() && idle(), "LAN enable autosave");
 if (fixture.snapshot.status.Address !== "127.0.0.1:8787") throw new Error("LAN setting applied before confirmation");
 await webAction("cancel");
 if (fixture.snapshot.settings.host !== "127.0.0.1" || lan().getAttribute("aria-checked") !== "false") throw new Error("LAN cancel did not restore scope");
 lan().click();
 await waitFor(() => fixture.snapshot.settings.host === "0.0.0.0" && webPending() && idle(), "LAN enable again");
 await webAction("apply");
 if (fixture.snapshot.status.Address !== "0.0.0.0:8787") throw new Error("LAN apply failed");
 web().click();
 await waitFor(() => fixture.snapshot.settings.web_enabled === false && webPending() && idle(), "Web disable autosave");
 if (!lan().disabled || !field("HTTP 端口").disabled || !field("公网地址").disabled || !webPending().textContent.includes("关闭 Web 入口")) throw new Error("Disabled Web controls or pending explanation are wrong");
 await webAction("cancel");
 if (web().getAttribute("aria-checked") !== "true" || lan().disabled || fixture.snapshot.settings.host !== "0.0.0.0") throw new Error("Web cancel discarded enablement or scope");
 web().click();
 await waitFor(() => fixture.snapshot.settings.web_enabled === false && webPending() && idle(), "Web disable again");
 await webAction("apply");
 if (fixture.snapshot.status.Address || !fixture.snapshot.status.Running || fixture.snapshot.status.TSNet.State !== "Running" || !fixture.snapshot.settings.relay_enabled || fixture.snapshot.settings.public_url !== original.public_url) throw new Error("Web disable affected another entry or discarded its URL");
 web().click();
 await waitFor(() => fixture.snapshot.settings.web_enabled && webPending() && idle(), "Web enable autosave");
 await webAction("apply");
 if (fixture.snapshot.status.Address !== "0.0.0.0:8787" || lan().disabled || field("公网地址").value !== original.public_url) throw new Error("Web enable did not restore configuration");
 const publicRow = field("公网地址").closest('.settings-row');
 const relayRow = [...document.querySelectorAll('.settings-row')].find(row => row.querySelector('.settings-row-label > span')?.textContent === "公网 Web 地址");
 const tailnetRow = [...document.querySelectorAll('.settings-row')].find(row => row.querySelector('.settings-row-label > span')?.textContent === "Tailnet HTTPS 地址");
 const qrButton = row => row.querySelector('.settings-address-qr');
 const activeRow = [...document.querySelectorAll('.settings-row')].find(row => row.querySelector('.settings-row-label > span')?.textContent === "当前监听地址");
 if (activeRow.querySelector('.settings-address-link').href !== "http://127.0.0.1:8787/" || publicRow.querySelector('.settings-address-link').href !== original.public_url + "/") throw new Error("Address link has the wrong URL");
 if ([...document.querySelectorAll('[data-page="Service"] button')].some(button => ["打开聊天", "在浏览器中打开"].includes(button.textContent.trim()))) throw new Error("Old global browser/chat actions are still visible");
 if (qrButton(publicRow).nextElementSibling !== field("公网地址") || relayRow.querySelector('.settings-copy-address').nextElementSibling !== qrButton(relayRow) || qrButton(tailnetRow).nextElementSibling !== tailnetRow.querySelector('.settings-copy-address')) throw new Error("QR icons are in the wrong position");
 const showQR = async (row, address) => {
  const count = fixture.commands.filter(command => command.kind === "address-qr").length;
  qrButton(row).click();
  await waitFor(() => document.querySelector('.settings-address-qr-dialog[open] img')?.naturalWidth > 0, "address QR image");
  const dialog = document.querySelector('.settings-address-qr-dialog[open]');
  const commands = fixture.commands.filter(command => command.kind === "address-qr");
  if (commands.length !== count + 1 || commands.at(-1).address !== address || dialog.querySelector('.settings-address-qr-url').textContent !== address || !idle()) throw new Error("QR uses the wrong address or blocks settings");
  const bounds = dialog.getBoundingClientRect();
  if (bounds.left < 0 || bounds.right > innerWidth || bounds.top < 0 || bounds.bottom > innerHeight || dialog.scrollWidth > dialog.clientWidth) throw new Error("QR dialog escaped viewport");
  if (typeof BarcodeDetector !== "undefined" && (await BarcodeDetector.getSupportedFormats()).includes("qr_code")) {
   const codes = await new BarcodeDetector({formats: ["qr_code"]}).detect(dialog.querySelector('img'));
   if (codes[0]?.rawValue !== address) throw new Error("QR scan did not reproduce the address");
  }
  return dialog;
 };
 const closeQR = async dialog => {
  dialog.querySelector('[aria-label="关闭"]').click();
  await waitFor(() => !document.querySelector('.settings-address-qr-dialog'), "close QR dialog");
 };
 let dialog = await showQR(publicRow, original.public_url);
 const qrCapture = await fetch('/capture?variant=qr', {method: 'POST'});
 if (!qrCapture.ok) throw new Error("QR capture failed");
 await closeQR(dialog);
 dialog = await showQR(relayRow, fixture.snapshot.status.Relay.WebURL);
 await closeQR(dialog);
 dialog = await showQR(tailnetRow, fixture.snapshot.status.TSNet.URL);
 dialog.dispatchEvent(new Event('cancel', {cancelable: true}));
 await waitFor(() => !document.querySelector('.settings-address-qr-dialog'), "dismiss QR with Escape");
 fixture.failNextQR = true;
 qrButton(publicRow).click();
 await waitFor(() => document.querySelector('.settings-address-qr-dialog [role="alert"]'), "QR failure feedback");
 await closeQR(document.querySelector('.settings-address-qr-dialog'));
 await closeQR(await showQR(publicRow, original.public_url));
 set(field("公网地址"), "not-a-url");
 await pause(30);
 if (!qrButton(publicRow).disabled) throw new Error("Invalid URL offered a QR code");
 set(field("公网地址"), original.public_url);
 await pause(30);
 await fetch('/report', {method: 'POST', body: JSON.stringify({ok:true, width:innerWidth})});
})().catch(async error => { await fetch('/report', {method:'POST', body:JSON.stringify({error:String(error)})}); });
