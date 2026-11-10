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
 const tab = key => document.querySelector(`[data-service-tab="${key}"]`);
 const panel = key => document.getElementById(tab(key).getAttribute('aria-controls'));
 const assertTab = key => {
  if (document.querySelectorAll('.settings-service-panel:not([hidden])').length !== 1 || panel(key).hidden || tab(key).getAttribute('aria-selected') !== 'true' || tab(key).tabIndex !== 0) throw new Error('Wrong active service tab: ' + key);
  for (const item of ['web', 'relay', 'tsnet', 'desktop']) {
   if (panel(item).getAttribute('aria-labelledby') !== tab(item).id || item !== key && (!panel(item).hidden || tab(item).tabIndex !== -1)) throw new Error('Service tab accessibility is out of sync');
  }
 };
 const selectTab = async key => {
  tab(key).focus();
  tab(key).click();
  await waitFor(() => !panel(key).hidden, key + ' tab');
  assertTab(key);
 };
 const set = (input, value) => {
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value").set.call(input, value);
  input.dispatchEvent(new Event("input", {bubbles: true}));
 };
 const pointerClick = async element => {
  const bounds = element.getBoundingClientRect();
  const params = {x: bounds.left + bounds.width / 2, y: bounds.top + bounds.height / 2, button: 'left', clickCount: 1};
  const response = await fetch('/smoke/browser', {method: 'POST', body: JSON.stringify([
   {method: 'Input.dispatchMouseEvent', params: {...params, type: 'mousePressed'}},
   {method: 'Input.dispatchMouseEvent', params: {...params, type: 'mouseReleased'}},
  ])});
  if (!response.ok) throw new Error('Pointer click failed: ' + await response.text());
 };
 await waitFor(() => document.querySelector('[aria-label="HTTP 端口"]'), "service settings");
 const original = structuredClone(fixture.snapshot.settings);
 // Tabs keep all drafts mounted and support the standard keyboard controls.
 assertTab('web');
 if ([...document.querySelectorAll('[role="tab"]')].map(item => item.textContent).join('|') !== 'Web|中继|Tailscale|远程桌面') throw new Error('Unexpected service tabs');
 const initialCommands = fixture.commands.length;
 for (const [key, target] of [['End', 'desktop'], ['ArrowRight', 'web'], ['ArrowLeft', 'desktop'], ['Home', 'web'], ['ArrowRight', 'relay'], ['ArrowRight', 'tsnet']]) {
  document.querySelector('[role="tab"][aria-selected="true"]').focus();
  document.activeElement.dispatchEvent(new KeyboardEvent('keydown', {key, bubbles: true}));
  await waitFor(() => !panel(target).hidden, 'keyboard ' + key);
  assertTab(target);
  if (document.activeElement !== tab(target)) throw new Error('Keyboard navigation did not move focus');
 }
 const keyInput = document.querySelector('input[aria-label="Tailscale Auth Key（可选）"]');
 set(keyInput, 'fixture-unsubmitted-key');
 await pause(30);
 await selectTab('desktop');
 if (!panel('desktop').querySelector('[aria-label="启用远程桌面"]') || panel('desktop').textContent.includes('HTTP 端口')) throw new Error('Remote desktop settings are not isolated');
 await selectTab('tsnet');
 if (keyInput !== document.querySelector('input[aria-label="Tailscale Auth Key（可选）"]') || keyInput.value !== 'fixture-unsubmitted-key') throw new Error('Tab switch discarded the Auth Key draft');
 set(keyInput, '');
 await selectTab('web');
 const portInput = field('HTTP 端口');
 portInput.focus();
 set(portInput, '');
 await pause(30);
 await selectTab('relay');
 await selectTab('web');
 if (portInput !== field('HTTP 端口') || portInput.value !== '' || fixture.commands.length !== initialCommands) throw new Error('Tab switch saved invalid data or discarded a draft');
 set(portInput, String(original.port));
 await pause(30);
 // A real click blurs the input before activating the bottom button. The blur
 // autosave must complete before Cancel/Apply without swallowing that click.
 for (const action of ['cancel', 'apply']) {
  portInput.focus(); set(portInput, '29875'); await pause(30); portInput.blur();
  await waitFor(() => fixture.snapshot.settings.port === 29875 && document.querySelector('[data-service-change="web"]') && idle(), action + ' pointer baseline');
  portInput.focus(); set(portInput, '29876'); await pause(30);
  fixture.holdNextCommand = 'save-settings';
  await pointerClick(document.querySelector(`[data-${action}-service-change="web"]`));
  await waitFor(() => fixture.releaseCommand, action + ' blur autosave started');
  fixture.releaseCommand(); fixture.releaseCommand = undefined;
  await waitFor(() => !document.querySelector('[data-service-change="web"]') && idle(), action + ' survived blur autosave');
  const expected = action === 'cancel' ? original.port : 29876;
  if (fixture.snapshot.settings.port !== expected || portInput.value !== String(expected)) throw new Error(action + ' used stale settings after blur');
 }
 // Restore the active baseline for the independent-entry checks below.
 portInput.focus(); set(portInput, String(original.port)); await pause(30); portInput.blur();
 await waitFor(() => fixture.snapshot.settings.port === original.port && document.querySelector('[data-service-change="web"]') && idle(), 'restore pointer baseline');
 document.querySelector('[data-apply-service-change="web"]').click();
 await waitFor(() => !document.querySelector('[data-service-change="web"]') && idle(), 'restored pointer baseline');
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
 await selectTab('tsnet');
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
 await selectTab('web');
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
 if (qrButton(publicRow).nextElementSibling !== field("公网地址")) throw new Error("Public Web QR button is not beside its address input");
 for (const [name, row] of [['Relay', relayRow], ['Tailscale', tailnetRow]]) {
  if (qrButton(row).nextElementSibling !== row.querySelector('.settings-copy-address')) throw new Error(name + " QR button is not before its copy button");
 }
 const showQR = async (row, address) => {
  const owner = row.closest('[role="tabpanel"]');
  await selectTab(document.getElementById(owner.getAttribute('aria-labelledby')).dataset.serviceTab);
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
 await selectTab('web');
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
 for (const key of ['web', 'relay', 'tsnet', 'desktop']) {
  await selectTab(key);
  document.querySelector('.settings-main').scrollTop = 0;
  await pause(30);
  if (document.scrollingElement.scrollWidth > innerWidth || panel(key).getBoundingClientRect().right > innerWidth) throw new Error('Service tabs overflow the viewport');
  const capture = await fetch('/capture?variant=' + key, {method: 'POST'});
  if (!capture.ok) throw new Error('Tab capture failed: ' + key);
 }
 await selectTab('tsnet');
 const tsnet = document.querySelector('[role="switch"][aria-label="启用内嵌 Tailscale"]');
 if (!tsnet.closest('.settings-row').textContent.includes('MagicDNS 和 HTTPS 证书')) throw new Error('Tailscale guide is not under the enable setting');
 tsnet.click();
 await waitFor(() => !fixture.snapshot.settings.tsnet_enabled && document.querySelector('[data-apply-service-change="tsnet"]') && idle(), 'disable Tailscale');
 document.querySelector('[data-apply-service-change="tsnet"]').click();
 await waitFor(() => fixture.snapshot.status.TSNet.State === 'Disabled' && idle(), 'apply Tailscale disable');
 const signIn = () => [...panel('tsnet').querySelectorAll('button')].find(button => button.textContent.trim() === '登录 Tailscale');
 await waitFor(() => signIn() && !signIn().disabled, 'login remains available while disabled');
 fixture.holdNextLogin = true;
 signIn().click();
 await waitFor(() => fixture.releaseLogin, 'disabled login request');
 await selectTab('web');
 await selectTab('tsnet');
 fixture.releaseLogin();
 await waitFor(() => fixture.snapshot.status.TSNet.AuthURL && signIn() && !signIn().disabled, 'disabled login completed');
 if (fixture.snapshot.settings.tsnet_enabled || fixture.snapshot.status.TSNet.URL || document.querySelector('[data-service-change="tsnet"]')) throw new Error('Login enabled Tailscale or lost the off state');
 const service = document.querySelector('[data-page="Service"]');
 const about = document.querySelector('[data-page="About"]');
 const startup = field('应用启动时开启 Spool 服务');
 if (startup.closest('.settings-group').querySelector('h2').textContent !== 'Spool 服务' || service.querySelector('[aria-label="任务完成后空闲释放时间（分钟）"]') || service.querySelector('[aria-label="历史缓存大小（MiB）"]')) throw new Error('Startup or runtime settings are on the wrong card');
 startup.click();
 await waitFor(() => fixture.snapshot.settings.auto_start === !original.auto_start && idle(), 'startup switch saved');
 document.querySelector('[data-section="About"]').click();
 await waitFor(() => !about.hidden && idle(), 'About runtime settings');
 const idleInput = about.querySelector('[aria-label="任务完成后空闲释放时间（分钟）"]');
 const historyInput = about.querySelector('[aria-label="历史缓存大小（MiB）"]');
 if (!idleInput || !historyInput || idleInput.closest('.settings-group') !== historyInput.closest('.settings-group')) throw new Error('Runtime settings did not move together');
 historyInput.focus(); set(historyInput, '128'); await pause(30); historyInput.blur();
 await waitFor(() => fixture.snapshot.settings.history_cache_mib === 128 && idle(), 'About cache saved on blur');
 idleInput.focus(); set(idleInput, ''); await pause(30);
 document.querySelector('[data-section="Service"]').click();
 await waitFor(() => !service.hidden, 'leave incomplete runtime draft');
 document.querySelector('[data-section="About"]').click();
 await waitFor(() => !about.hidden, 'return to incomplete runtime draft');
 if (idleInput.value !== '' || fixture.snapshot.settings.idle_minutes !== original.idle_minutes) throw new Error('Runtime draft was discarded or saved before valid');
 idleInput.focus(); set(idleInput, '30'); await pause(30); idleInput.blur();
 await waitFor(() => fixture.snapshot.settings.idle_minutes === 30 && idle(), 'About runtime saved immediately');
 if (document.querySelector('[data-service-change="runtime"]') || fixture.snapshot.status.RestartRequired) throw new Error('Runtime changes require an apply action');
 document.querySelector('[data-section="Service"]').click();
 await waitFor(() => !service.hidden, 'leave saved runtime settings');
 if (document.querySelector('[data-service-change="runtime"]') || fixture.commands.some(command => command.kind === 'apply-service-changes' && command.target === 'runtime')) throw new Error('Runtime changes requested an apply action');
 if (idleInput.value !== '30' || fixture.snapshot.settings.history_cache_mib !== 128 || fixture.snapshot.settings.auto_start !== !original.auto_start) throw new Error('Runtime save lost its About draft or unrelated settings');
 document.querySelector('[data-section="About"]').click();
 await waitFor(() => !about.hidden, 'About after runtime save');
 idleInput.scrollIntoView({block: 'center', behavior: 'instant'});
 await fetch('/capture?variant=about', {method: 'POST'});
 await fetch('/report', {method: 'POST', body: JSON.stringify({ok:true, width:innerWidth})});
})().catch(async error => { await fetch('/report', {method:'POST', body:JSON.stringify({error:String(error)})}); });
