(async () => {
 const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
 const waitFor = async (check, label) => {
  for (let i = 0; i < 100; i++) { if (check()) return; await pause(30); }
  throw new Error("Timed out: " + label);
 };
 const fixture = window.smokeSettings;
 const about = () => document.querySelector('[data-page="About"]');
 const input = label => about().querySelector(`[aria-label="${label}"]`);
 const idle = () => !document.querySelector('.settings-page-controls[aria-busy="true"]');
 const commit = async (label, value, key) => {
  const field = input(label);
  field.focus();
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value").set.call(field, value);
  field.dispatchEvent(new Event("input", {bubbles: true}));
  await pause(30);
  field.blur();
  await waitFor(() => fixture.snapshot.settings[key] === Number(value) && idle(), key + ' saved');
  if (document.querySelector('.settings-restart-bar') || fixture.snapshot.status.RestartRequired || fixture.snapshot.status.PendingChanges.length) throw new Error(key + ' requires an apply action');
 };
 await waitFor(() => about() && !about().hidden && input('任务完成后空闲释放时间（分钟）'), 'runtime settings');
 const original = structuredClone(fixture.snapshot.settings);
 const initialCommands = fixture.commands.length;
 for (const value of ['30', '1', String(original.idle_minutes)]) {
  await commit('任务完成后空闲释放时间（分钟）', value, 'idle_minutes');
 }
 await commit('历史缓存大小（MiB）', '128', 'history_cache_mib');
 const commands = fixture.commands.slice(initialCommands);
 if (commands.length !== 4 || commands.some(command => command.kind !== 'save-settings')) throw new Error('Runtime edits requested another action');
 if (fixture.snapshot.settings.port !== original.port || fixture.snapshot.settings.auto_start !== original.auto_start) throw new Error('Runtime edits changed unrelated settings');
 if (!about().textContent.includes('修改立即生效，无需重启服务，也不会中断正在执行的任务。')) throw new Error('Runtime settings do not explain immediate application');
 document.querySelector('[data-section="Service"]').click();
 await waitFor(() => about().hidden && idle(), 'leave runtime settings');
 if (document.querySelector('.settings-restart-bar')) throw new Error('Runtime edits left pending changes on another page');
 document.querySelector('[data-section="About"]').click();
 await waitFor(() => !about().hidden && idle(), 'return to runtime settings');
 if (input('任务完成后空闲释放时间（分钟）').value !== String(original.idle_minutes) || input('历史缓存大小（MiB）').value !== '128') throw new Error('Runtime values changed after navigation');
 await fetch('/capture', {method: 'POST'});
 await fetch('/report', {method: 'POST', body: JSON.stringify({ok: true, width: innerWidth})});
})().catch(async error => { await fetch('/report', {method: 'POST', body: JSON.stringify({error: String(error)})}); });
