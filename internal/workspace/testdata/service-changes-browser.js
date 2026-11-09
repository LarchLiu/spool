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
  ...original, port: 29876, tsnet_hostname: "pending-node", relay_enabled: true, relay_url: "https://relay.example.test", relay_lan_port: 28082,
 }});
 refresh();
 await waitFor(() => pending().length === 3, "three pending entries");
 const bar = document.querySelector('.settings-restart-bar');
 if (!bar.textContent.includes("本地 Web 入口") || !bar.textContent.includes("Spool 中继") || !bar.textContent.includes("Tailscale") || !bar.textContent.includes("正在执行的任务继续运行")) throw new Error("Missing entry-specific restart descriptions");
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
 await fetch('/report', {method: 'POST', body: JSON.stringify({ok:true, width:innerWidth})});
})().catch(async error => { await fetch('/report', {method:'POST', body:JSON.stringify({error:String(error)})}); });
