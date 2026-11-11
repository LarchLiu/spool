async function (expectedUser) {
  const assert = (value, message) => { if (!value) throw new Error(message); };
  const wait = async (condition, message) => {
    const deadline = Date.now() + 10000;
    while (!await condition()) {
      if (Date.now() > deadline) throw new Error(message);
      await new Promise(resolve => setTimeout(resolve, 40));
    }
  };
  const base = location.pathname;
  window.relayStep = 'login';
  assert(base === '/' && /^[a-z2-7]{52}\./.test(location.hostname), "missing isolated server hostname");
  await wait(() => document.querySelector('input[name="username"]'), "login form did not load");
  const set = (name, value) => {
    const input = document.querySelector(`input[name="${name}"]`);
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set.call(input, value);
    input.dispatchEvent(new Event('input', { bubbles: true }));
  };
  set('username', 'alice');
  set('password', 'test-password-2026');
  document.querySelector('input[name="username"]').form.requestSubmit();
  await wait(() => !document.querySelector('input[name="username"]') && window.relayProbe.ready, "login and live workspace did not connect");
  window.relayStep = 'identity';
  const identity = await (await fetch('./api/me')).json();
  assert(identity.user?.id === expectedUser, "session belongs to another server");
  window.relayStep = 'worker';
  await wait(() => navigator.serviceWorker.controller, "service worker did not take control");
  const registration = await navigator.serviceWorker.getRegistration(location.href);
  assert(registration?.scope === location.origin + base, "worker scope escaped server path");
  assert(new URL(registration.active.scriptURL).pathname === base + 'sw.js', "wrong worker script");
  window.relayStep = 'manifest';
  const manifestURL = document.querySelector('link[rel="manifest"]').href;
  const manifest = await (await fetch(manifestURL)).json();
  // The Go driver checks the browser's processed ID with Page.getAppManifest.
  // Unlike scope/start_url, an explicit relative id resolves from the origin.
  assert(new URL(manifest.scope, manifestURL).pathname === base, "wrong manifest scope");
  assert(new URL(manifest.start_url, manifestURL).pathname === base, "wrong manifest start URL");
  assert(window.relayProbe.sockets.every(url => new URL(url, location.href).pathname.startsWith(base)), "WebSocket escaped path");
  window.relayStep = 'chat';
  const projects = await (await fetch('./api/projects')).json();
  const created = await fetch('./api/chats', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': identity.csrf },
    body: JSON.stringify({ project_id: projects[0].id, backend: 'codex' }),
  });
  assert(created.status === 201, "chat creation failed");
  const chat = await created.json();
  window.relayStep = 'stream';
  // The app opens the same prefixed stream when a chat is selected. Check the
  // native EventSource in this browser too, including cookies and SSE flushing.
  await new Promise((resolve, reject) => {
    const stream = new EventSource(base + 'api/chats/' + chat.id + '/events');
    const timeout = setTimeout(() => { stream.close(); reject(new Error('SSE timed out')); }, 5000);
    stream.addEventListener('snapshot', () => { clearTimeout(timeout); stream.close(); resolve(); });
    stream.onerror = () => { clearTimeout(timeout); stream.close(); reject(new Error('SSE failed')); };
  });
  window.relayStep = 'cache';
  const resources = performance.getEntriesByType('resource').map(entry => new URL(entry.name)).filter(url => url.origin === location.origin);
  assert(resources.every(url => url.pathname.startsWith(base)), "a resource or API escaped the mount: " + resources.filter(url => !url.pathname.startsWith(base)).map(url => url.pathname));
  const cacheNames = (await caches.keys()).filter(name => name.startsWith('spool-chat-shell:' + base + ':'));
  assert(cacheNames.length === 1, "missing scoped offline cache");
  const cached = await (await caches.open(cacheNames[0])).keys();
  assert(cached.every(request => new URL(request.url).pathname.startsWith(base) && !new URL(request.url).pathname.startsWith(base + 'api/')), "cached private data or another server");
  return { ok: true, scope: registration.scope, resources: resources.length, live: window.relayProbe.ready, streams: window.relayProbe.streams.length, caches: cacheNames.length };
}
