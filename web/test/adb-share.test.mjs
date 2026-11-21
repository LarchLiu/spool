import assert from "node:assert/strict";
import test from "node:test";
import { openADBLink } from "../.test-build/adb-usb.js";
import { createADBShare } from "../.test-build/adb-share.js";

const text = (value) => new TextEncoder().encode(value);

// One ADB packet as it travels: a 24-byte header, then the payload.
function packet(command, payload = new Uint8Array(0)) {
  const bytes = new Uint8Array(24 + payload.length);
  const view = new DataView(bytes.buffer);
  const word =
    [...command].reduce(
      (value, letter, index) => value | (letter.charCodeAt(0) << (8 * index)),
      0,
    ) >>> 0;
  view.setUint32(0, word, true);
  view.setUint32(12, payload.length, true);
  view.setUint32(20, (word ^ 0xffffffff) >>> 0, true);
  bytes.set(payload, 24);
  return bytes;
}

const named = (name, message) => Object.assign(new Error(message), { name });

// Lets everything that does not wait for a timer, the phone or the server run.
const settle = () => new Promise((resolve) => setImmediate(resolve));

// A phone with USB debugging on, as WebUSB presents it.
function phone({ serial = "R5CT900", packetSize = 512 } = {}) {
  const alternate = {
    alternateSetting: 0,
    interfaceClass: 0xff,
    interfaceSubclass: 0x42,
    interfaceProtocol: 1,
    endpoints: [
      { endpointNumber: 1, direction: "in", type: "bulk", packetSize },
      { endpointNumber: 2, direction: "out", type: "bulk", packetSize },
    ],
  };
  const storage = {
    alternateSetting: 0,
    interfaceClass: 0xff,
    interfaceSubclass: 0xff,
    interfaceProtocol: 0,
    endpoints: [],
  };
  const configuration = {
    configurationValue: 1,
    interfaces: [
      { interfaceNumber: 0, alternate: storage, alternates: [storage] },
      { interfaceNumber: 3, alternate, alternates: [alternate] },
    ],
  };
  const queued = [];
  let reader;
  const device = {
    vendorId: 0x18d1,
    productId: 0x4ee7,
    serialNumber: serial,
    productName: "Pixel",
    manufacturerName: "Google",
    configuration,
    configurations: [configuration],
    opened: false,
    claimed: undefined,
    halts: 0,
    // What the phone received, one entry per USB transfer.
    received: [],
    refuses: undefined,
    async open() {
      device.opened = true;
    },
    async close() {
      device.opened = false;
      device.claimed = undefined;
      reader?.reject(named("AbortError", "The transfer was cancelled."));
      reader = undefined;
    },
    async selectConfiguration() {},
    async claimInterface(number) {
      if (device.refuses) throw device.refuses;
      device.claimed = number;
    },
    async selectAlternateInterface() {},
    async clearHalt() {
      device.halts++;
    },
    transferIn(endpoint, length) {
      assert.equal(endpoint, 1);
      assert.equal(device.claimed, 3, "read without the debugging interface");
      const next = queued.shift();
      if (next) return Promise.resolve(next(length));
      return new Promise((resolve, reject) => {
        reader = { resolve: (transfer) => resolve(transfer(length)), reject };
      });
    },
    async transferOut(endpoint, data) {
      assert.equal(endpoint, 2);
      assert.equal(device.claimed, 3, "write without the debugging interface");
      device.received.push(Uint8Array.from(data));
      return { status: "ok" };
    },
    // One USB transfer from the phone.
    transfers(bytes, status = "ok") {
      const transfer = (length) =>
        bytes.length > length
          ? { status: "babble" }
          : { status, data: new DataView(Uint8Array.from(bytes).buffer) };
      if (reader) {
        const waiting = reader;
        reader = undefined;
        waiting.resolve(transfer);
      } else queued.push(transfer);
    },
    // One packet from the phone: its header, then its payload.
    sends(bytes) {
      device.transfers(bytes.subarray(0, 24));
      if (bytes.length > 24) device.transfers(bytes.subarray(24));
    },
    unread: () => queued.length,
    unplugged() {
      device.opened = false;
      device.claimed = undefined;
      reader?.reject(named("NotFoundError", "The device was disconnected."));
      reader = undefined;
    },
  };
  return device;
}

test("packets reach the phone as adb sends them over a cable", async () => {
  const device = phone();
  const link = await openADBLink(device);
  assert.equal(device.claimed, 3);
  const short = packet("WRTE", text("shell:ls"));
  await link.write(short.slice(0, 24), short.slice(24));
  // A header and its payload are separate transfers.
  assert.deepEqual(device.received, [short.slice(0, 24), short.slice(24)]);

  device.received.length = 0;
  const empty = packet("OKAY");
  await link.write(empty.slice(0, 24), empty.slice(24));
  assert.deepEqual(device.received, [empty]);

  // A payload that fills its last USB packet ends with an empty transfer.
  device.received.length = 0;
  const full = packet("WRTE", new Uint8Array(1024).fill(7));
  await link.write(full.slice(0, 24), full.slice(24));
  assert.deepEqual(
    device.received.map((transfer) => transfer.length),
    [24, 1024, 0],
  );
  await link.close();
  assert.equal(device.opened, false);
});

test("packets from the phone are read whole, whatever an earlier session left behind", async () => {
  const device = phone();
  const link = await openADBLink(device);
  // The rest of a payload nobody read, and 24 bytes that are no header.
  device.transfers(new Uint8Array(300).fill(9));
  device.transfers(new Uint8Array(24).fill(9));
  const first = packet("CNXN", text("device::ro.product.name=pixel"));
  device.sends(first);
  // A payload may arrive in several transfers, and a stalled endpoint recovers.
  const second = packet("WRTE", new Uint8Array(1500).fill(3));
  device.transfers(second.subarray(0, 24));
  device.transfers(new Uint8Array(0), "stall");
  device.transfers(second.subarray(24, 1048));
  device.transfers(second.subarray(1048));
  const third = packet("OKAY");
  device.sends(third);
  assert.deepEqual(await link.read(), first);
  assert.deepEqual(await link.read(), second);
  assert.deepEqual(await link.read(), third);
  assert.equal(device.halts, 1);

  // A phone that sends more than it announced is not relayed.
  const lying = packet("WRTE", new Uint8Array(8));
  device.transfers(lying.subarray(0, 24));
  device.transfers(new Uint8Array(40));
  await assert.rejects(link.read(), { name: "ADBLinkError", failure: "transfer" });
});

test("a debugging interface held by another program is reported as busy", async () => {
  const device = phone();
  device.refuses = named("NetworkError", "Unable to claim interface.");
  await assert.rejects(openADBLink(device), {
    failure: "busy",
    message: "Unable to claim interface.",
  });
  assert.equal(device.opened, false);

  const plain = phone();
  plain.configurations[0].interfaces.pop();
  await assert.rejects(openADBLink(plain), { failure: "no_interface" });
});

// The browser's USB devices: what is plugged in and what the user picks.
function usbHost(device) {
  const listeners = { connect: new Set(), disconnect: new Set() };
  const usb = {
    present: new Set([device]),
    // The device picked in the browser's list; none closes the list.
    picks: device,
    requests: [],
    async requestDevice(options) {
      usb.requests.push(options);
      if (!usb.picks) throw named("NotFoundError", "No device selected.");
      return usb.picks;
    },
    async getDevices() {
      return [...usb.present];
    },
    addEventListener: (type, listener) => listeners[type].add(listener),
    removeEventListener: (type, listener) => listeners[type].delete(listener),
    listening: () => listeners.connect.size + listeners.disconnect.size,
    unplug(gone) {
      usb.present.delete(gone);
      gone.unplugged();
      for (const listener of [...listeners.disconnect]) listener({ device: gone });
    },
    plug(back) {
      usb.present.add(back);
      for (const listener of [...listeners.connect]) listener({ device: back });
    },
  };
  return usb;
}

function fixture(t, device = phone()) {
  const usb = usbHost(device);
  const sockets = [];
  const timers = [];
  let time = 0;
  // The Spool host as this client reaches it.
  const host = {
    reachable: true,
    availability: { available: true },
    checks: 0,
  };
  const share = createADBShare({
    usb,
    socket() {
      const socket = {
        binaryType: "blob",
        bufferedAmount: 0,
        sent: [],
        closed: false,
        onopen: null,
        onmessage: null,
        onerror: null,
        onclose: null,
        send(data) {
          assert.equal(socket.closed, false, "sent on a closed stream");
          socket.sent.push(
            typeof data === "string" ? JSON.parse(data) : Uint8Array.from(data),
          );
        },
        close() {
          socket.closed = true;
        },
        // The host's side of the stream.
        accepts: () => socket.onopen?.({}),
        says: (message) => socket.onmessage?.({ data: JSON.stringify(message) }),
        relays: (bytes) =>
          socket.onmessage?.({ data: Uint8Array.from(bytes).buffer }),
        drops: () => socket.onclose?.({}),
      };
      sockets.push(socket);
      return socket;
    },
    csrf: () => "session-token",
    async availability() {
      host.checks++;
      if (!host.reachable) throw new TypeError("Failed to fetch");
      return host.availability;
    },
    schedule(callback, milliseconds) {
      const timer = { at: time + milliseconds, callback };
      timers.push(timer);
      return () => {
        const index = timers.indexOf(timer);
        if (index >= 0) timers.splice(index, 1);
      };
    },
    now: () => time,
  });
  t.after(() => share.dispose());
  const f = {
    device,
    usb,
    host,
    share,
    sockets,
    async passes(milliseconds) {
      const end = time + milliseconds;
      for (;;) {
        timers.sort((a, b) => a.at - b.at);
        if (!timers.length || timers[0].at > end) break;
        const timer = timers.shift();
        time = timer.at;
        timer.callback();
        await settle();
      }
      time = end;
      await settle();
    },
    // Starts sharing and plays the host until adb has the phone online.
    async online() {
      await share.start();
      const socket = sockets.at(-1);
      f.accept(socket);
      return socket;
    },
    accept(socket, serial = "127.0.0.1:41000") {
      socket.accepts();
      socket.says({ type: "ready", serial });
      socket.says({ type: "state", state: "connecting" });
      socket.says({ type: "state", state: "online" });
    },
  };
  return f;
}

test("a phone plugged into this computer becomes a device of the server's adb", async (t) => {
  const f = fixture(t);
  await f.share.start();
  assert.deepEqual(f.usb.requests, [
    { filters: [{ classCode: 0xff, subclassCode: 0x42, protocolCode: 1 }] },
  ]);
  const [socket] = f.sockets;
  assert.equal(socket.binaryType, "arraybuffer");
  assert.equal(f.share.state().phase, "connecting");
  assert.deepEqual(socket.sent, []);
  socket.accepts();
  assert.deepEqual(socket.sent, [
    {
      type: "attach",
      csrf: "session-token",
      transport: "usb",
      device: "usb-18d1-4ee7-R5CT900",
    },
  ]);
  socket.says({ type: "ready", serial: "127.0.0.1:41000" });
  socket.says({ type: "state", state: "connecting" });

  // adb's bytes arrive cut wherever the network cut them.
  const connect = packet("CNXN", text("host::features=shell_v2,cmd"));
  const open = packet("OPEN", text("shell:"));
  const stream = new Uint8Array([...connect, ...open]);
  for (const [from, to] of [
    [0, 10],
    [10, 31],
    [31, 70],
    [70, stream.length],
  ])
    socket.relays(stream.subarray(from, to));
  await settle();
  assert.deepEqual(f.device.received, [
    connect.slice(0, 24),
    connect.slice(24),
    open.slice(0, 24),
    open.slice(24),
  ]);

  // The phone's answers travel as whole packets.
  const challenge = packet("AUTH", new Uint8Array(20).fill(5));
  f.device.sends(challenge);
  await settle();
  assert.deepEqual(socket.sent.at(-1), challenge);

  socket.says({ type: "state", state: "unauthorized" });
  assert.deepEqual(f.share.state(), {
    phase: "unauthorized",
    device: "Pixel",
    serial: "127.0.0.1:41000",
  });
  socket.says({ type: "state", state: "online" });
  assert.deepEqual(f.share.state(), {
    phase: "online",
    device: "Pixel",
    serial: "127.0.0.1:41000",
  });
});

test("the phone is read no faster than the network takes its data", async (t) => {
  const f = fixture(t);
  const socket = await f.online();
  const sent = socket.sent.length;
  socket.bufferedAmount = 64 << 20;
  const first = packet("WRTE", text("first"));
  const second = packet("WRTE", text("second"));
  f.device.sends(first);
  f.device.sends(second);
  await f.passes(1000);
  assert.equal(socket.sent.length, sent);
  // The second packet stays in the phone, whose own flow control then holds.
  assert.equal(f.device.unread(), 2);
  socket.bufferedAmount = 0;
  await f.passes(1000);
  assert.deepEqual(socket.sent.slice(sent), [first, second]);
});

test("sharing survives a lost connection and a restarting server", async (t) => {
  const f = fixture(t);
  const first = await f.online();
  first.drops();
  await settle();
  assert.deepEqual(f.share.state(), {
    phase: "waiting",
    device: "Pixel",
    code: "network",
  });
  assert.equal(f.device.opened, false);

  // While the server cannot be reached nothing is known about access.
  f.host.reachable = false;
  await f.passes(10_000);
  assert.ok(f.host.checks >= 2);
  assert.equal(f.sockets.length, 1);
  assert.equal(f.share.state().phase, "waiting");

  f.host.reachable = true;
  await f.passes(40_000);
  assert.equal(f.sockets.length, 2);
  const second = f.sockets[1];
  f.accept(second);
  // The same key, so the server gives the phone the serial agents already use.
  assert.deepEqual(second.sent[0], first.sent[0]);
  assert.equal(f.share.state().phase, "online");
  assert.equal(f.device.opened, true);

  for (const code of ["stopping", "adb_closed"]) {
    const before = f.sockets.length;
    f.sockets.at(-1).says({ type: "error", code, message: "diagnostic" });
    await settle();
    assert.deepEqual(f.share.state(), {
      phase: "waiting",
      device: "Pixel",
      code,
    });
    await f.passes(40_000);
    assert.equal(f.sockets.length, before + 1);
    f.accept(f.sockets.at(-1));
    assert.equal(f.share.state().phase, "online");
  }
});

test("a server that goes quiet is treated as a lost connection", async (t) => {
  const f = fixture(t);
  const socket = await f.online();
  await f.passes(30_000);
  socket.says({ type: "alive" });
  await f.passes(30_000);
  assert.equal(f.share.state().phase, "online");
  await f.passes(45_000);
  assert.equal(socket.closed, true);
  assert.notEqual(f.share.state().phase, "online");
});

test("sharing ends for good once the server withdraws it", async (t) => {
  for (const code of ["revoked", "replaced", "busy", "connect_failed"]) {
    const f = fixture(t);
    const socket = await f.online();
    socket.says({ type: "error", code, message: "diagnostic" });
    await settle();
    assert.deepEqual(f.share.state(), {
      phase: "failed",
      device: "Pixel",
      code,
      detail: "diagnostic",
    });
    assert.equal(socket.closed, true);
    assert.equal(f.device.opened, false);
    await f.passes(120_000);
    assert.equal(f.sockets.length, 1, code);
    assert.equal(f.host.checks, 0, code);
  }

  // A stream the server refuses to open looks like a lost connection until
  // the server says that sharing is no longer available.
  const f = fixture(t);
  const socket = await f.online();
  f.host.availability = { available: false, reason: "disabled" };
  socket.drops();
  await f.passes(120_000);
  assert.deepEqual(f.share.state(), {
    phase: "failed",
    device: "Pixel",
    code: "disabled",
    detail: undefined,
  });
  assert.equal(f.sockets.length, 1);
  assert.equal(f.host.checks, 1);
});

test("bytes that are not adb's never reach the phone", async (t) => {
  const f = fixture(t);
  const socket = await f.online();
  socket.relays(new Uint8Array(24).fill(1));
  await settle();
  assert.deepEqual(f.device.received, []);
  assert.equal(f.share.state().phase, "failed");
  assert.equal(socket.closed, true);
});

test("an unplugged phone is shared again when it comes back", async (t) => {
  const f = fixture(t);
  const first = await f.online();
  f.usb.unplug(f.device);
  await settle();
  assert.deepEqual(f.share.state(), {
    phase: "waiting",
    device: "Pixel",
    code: "device_gone",
  });
  assert.equal(first.closed, true);
  await f.passes(120_000);
  assert.equal(f.sockets.length, 1);
  assert.equal(f.share.state().phase, "waiting");

  // After adb root or a reboot the browser presents the phone anew. Another
  // device does not take its place.
  const other = phone({ serial: "OTHER" });
  f.usb.plug(other);
  await settle();
  assert.equal(f.sockets.length, 1);
  const again = phone();
  f.usb.plug(again);
  await settle();
  assert.equal(f.sockets.length, 2);
  f.accept(f.sockets[1]);
  assert.deepEqual(f.sockets[1].sent[0], first.sent[0]);
  assert.equal(again.opened, true);
  assert.equal(other.opened, false);
  assert.equal(f.share.state().phase, "online");
});

test("an unidentified phone is never replaced by another authorized USB device", async (t) => {
  const f = fixture(t, phone({ serial: null }));
  const socket = await f.online();
  // Network interruptions may still recover with the very same device object.
  socket.drops();
  await f.passes(2_000);
  assert.deepEqual(f.sockets[1].sent, []);
  f.accept(f.sockets[1]);
  assert.equal(f.sockets[1].sent[0].device, socket.sent[0].device);

  f.usb.unplug(f.device);
  const other = phone({ serial: null });
  f.usb.plug(other);
  await f.passes(60_000);
  assert.equal(f.share.state().phase, "failed");
  assert.equal(f.share.state().code, "reselect_device");
  assert.equal(other.opened, false);
  assert.equal(f.sockets.length, 2);

  f.usb.picks = other;
  const selected = await f.online();
  assert.notEqual(selected.sent[0].device, socket.sent[0].device);
});

test("automatic reattachment rejects ambiguous serials", async (t) => {
  const f = fixture(t);
  await f.online();
  f.usb.unplug(f.device);
  const one = phone();
  const two = phone();
  f.usb.plug(one);
  f.usb.plug(two);
  await settle();
  assert.equal(f.share.state().code, "reselect_device");
  assert.equal(f.sockets.length, 1);
  assert.equal(one.opened, false);
  assert.equal(two.opened, false);
});

test("a duplicate serial seen during sharing cannot take over after an unplug", async (t) => {
  const f = fixture(t);
  await f.online();
  const duplicate = phone();
  f.usb.plug(duplicate);
  f.usb.unplug(f.device);
  await f.passes(2_000);
  assert.equal(f.share.state().code, "reselect_device");
  assert.equal(f.sockets.length, 1);
  assert.equal(duplicate.opened, false);
});

test("restarting waits for a canceled USB open and its cleanup", async (t) => {
  const f = fixture(t);
  const { open, close } = f.device;
  let finishOpen, finishClose;
  const opening = new Promise((resolve) => { finishOpen = resolve; });
  const closing = new Promise((resolve) => { finishClose = resolve; });
  let opens = 0;
  f.device.open = async () => {
    if (++opens === 1) await opening;
    await open();
  };
  f.device.close = async () => { await closing; await close(); };
  t.after(() => { finishOpen(); finishClose(); });

  const abandoned = f.share.start();
  await settle();
  assert.equal(opens, 1);
  f.share.stop();
  const restarted = f.share.start();
  await settle();
  assert.equal(opens, 1);
  finishOpen();
  await settle();
  assert.equal(opens, 1);
  assert.equal(f.sockets.length, 0);
  finishClose();
  await Promise.all([abandoned, restarted]);
  assert.equal(opens, 2);
  assert.equal(f.sockets.length, 1);
  f.accept(f.sockets[0]);
  assert.equal(f.share.state().phase, "online");
  assert.equal(f.device.opened, true);
});

test("starting fails clearly when the phone cannot be opened, and quietly when nothing is chosen", async (t) => {
  const f = fixture(t);
  f.usb.picks = undefined;
  await f.share.start();
  assert.deepEqual(f.share.state(), { phase: "idle" });

  f.usb.picks = f.device;
  f.device.refuses = named("NetworkError", "Unable to claim interface.");
  await f.share.start();
  assert.deepEqual(f.share.state(), {
    phase: "failed",
    device: "Pixel",
    code: "usb_busy",
    detail: "Unable to claim interface.",
  });
  assert.equal(f.device.opened, false);
  assert.equal(f.sockets.length, 0);

  // The user frees the interface and tries again.
  f.device.refuses = undefined;
  await f.online();
  assert.equal(f.share.state().phase, "online");
});

test("stopping gives the phone back and nothing reaches it afterwards", async (t) => {
  const f = fixture(t);
  const socket = await f.online();
  const phases = [];
  f.share.subscribe(() => phases.push(f.share.state().phase));
  f.share.stop();
  await settle();
  assert.deepEqual(f.share.state(), { phase: "idle" });
  assert.deepEqual(phases, ["idle"]);
  assert.equal(socket.closed, true);
  assert.equal(f.device.opened, false);
  socket.relays(packet("OPEN", text("shell:")));
  socket.says({ type: "state", state: "online" });
  socket.drops();
  await f.passes(120_000);
  assert.deepEqual(f.device.received, []);
  assert.deepEqual(phases, ["idle"]);
  assert.equal(f.sockets.length, 1);

  // Stopping while waiting to resume cancels the retry.
  const next = await f.online();
  next.drops();
  await settle();
  assert.equal(f.share.state().phase, "waiting");
  f.share.stop();
  await f.passes(120_000);
  assert.equal(f.sockets.length, 2);
  f.share.dispose();
  assert.equal(f.usb.listening(), 0);
});
