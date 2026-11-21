import {
  ADBLinkError,
  adbHeaderSize,
  adbInterface,
  adbMaxPayload,
  adbPayloadLength,
  hasADBInterface,
  openADBLink,
  type ADBLink,
  type USBDeviceLike,
  type USBLike,
} from "./adb-usb";

// Sharing a phone that is plugged into this computer with adb on the Spool
// host. One WebSocket carries the bytes of the phone's ADB connection; the
// host's adb sees it as the device 127.0.0.1:<port>.
//
// Sharing stays on until the user stops it, the way a cable stays plugged in:
// a lost connection, a restarted adb server or a phone that re-enumerates
// (adb root, a reboot) resumes by itself. What ends it for good is the user,
// the host withdrawing access, or a problem only the user can fix.

export type ADBShareAvailability = { available: boolean; reason?: string };

export type ADBShareState = {
  phase:
    | "idle"
    // The browser is asking which device to use.
    | "choosing"
    // Opening the phone or reaching adb on the host.
    | "connecting"
    // The phone is asking its user to allow the host's adb key.
    | "unauthorized"
    | "online"
    // Interrupted; sharing resumes by itself.
    | "waiting"
    // Ended; only the user can start it again.
    | "failed";
  // The browser's name for the device; may be empty.
  device?: string;
  // How programs on the host address the device: adb -s <serial>.
  serial?: string;
  // Why sharing is waiting or failed.
  code?: string;
  // The host's diagnostic, for codes this client has no text for.
  detail?: string;
};

export type ADBShareEnvironment = {
  usb: USBLike;
  // Opens the bridge stream to the host.
  socket(): WebSocket;
  csrf(): string;
  // Whether this user may share right now. Rejects when the host cannot be
  // reached, which says nothing about access.
  availability(): Promise<ADBShareAvailability>;
  // Tests replace the clock.
  schedule?(callback: () => void, milliseconds: number): () => void;
  now?(): number;
};

export type ADBShare = {
  state(): ADBShareState;
  subscribe(listener: () => void): () => void;
  // Must run in a user gesture: the browser asks which device to share.
  start(): Promise<void>;
  stop(): void;
  dispose(): void;
};

// The host says something at least every 15 seconds.
const silenceLimit = 45_000;
const retryBase = 1_000;
const retryLimit = 30_000;
// A share that held this long was working; the next interruption starts over
// with a short delay.
const steadyAfter = 30_000;
// Stop reading the phone while this much is still waiting for the network.
const uploadHighWater = 8 << 20;

// The host ends a stream with one of these when trying again can help.
const resumable = new Set(["adb_closed", "stopping"]);

type Target = {
  selected: USBDeviceLike;
  vendorId: number;
  productId: number;
  serial: string;
  ambiguous: boolean;
  name: string;
  // Stable for identified phones, or unique to the user's current selection.
  key: string;
};

function identify(device: USBDeviceLike): Target {
  const serial = device.serialNumber?.trim() ? device.serialNumber : "";
  const hex = (value: number) => value.toString(16).padStart(4, "0");
  return {
    selected: device,
    vendorId: device.vendorId,
    productId: device.productId,
    serial,
    ambiguous: false,
    name: (device.productName || device.manufacturerName || "").trim(),
    key: serial
      ? `usb-${hex(device.vendorId)}-${hex(device.productId)}-${serial.replace(/[^A-Za-z0-9._-]/g, "_")}`.slice(
          0,
          96,
        )
      : `usb-selection-${crypto.randomUUID()}`,
  };
}

function sameSerial(target: Target, device: USBDeviceLike) {
  return (
    target.serial !== "" &&
    device.vendorId === target.vendorId &&
    device.productId === target.productId &&
    device.serialNumber === target.serial
  );
}

// Bytes from the host arrive in arbitrary pieces; the phone needs whole
// headers and payloads.
class ByteQueue {
  private chunks: Uint8Array[] = [];
  private size = 0;

  push(chunk: Uint8Array) {
    if (chunk.byteLength === 0) return;
    this.chunks.push(chunk);
    this.size += chunk.byteLength;
  }

  take(length: number): Uint8Array<ArrayBuffer> | undefined {
    if (this.size < length) return undefined;
    const result = new Uint8Array(length);
    for (let filled = 0; filled < length;) {
      const chunk = this.chunks[0];
      const part = Math.min(chunk.byteLength, length - filled);
      result.set(chunk.subarray(0, part), filled);
      filled += part;
      if (part === chunk.byteLength) this.chunks.shift();
      else this.chunks[0] = chunk.subarray(part);
    }
    this.size -= length;
    return result;
  }
}

type Live = {
  run: number;
  device: USBDeviceLike;
  link: ADBLink;
  socket: WebSocket;
  ended: boolean;
  serial?: string;
  onlineAt?: number;
  fromHost: ByteQueue;
  header?: Uint8Array<ArrayBuffer>;
  writing: boolean;
  quiet?: () => void;
};

type Ending =
  | { by: "host"; code: string; detail?: string }
  | { by: "network" }
  | { by: "phone" };

export function createADBShare(environment: ADBShareEnvironment): ADBShare {
  const schedule =
    environment.schedule ??
    ((callback, milliseconds) => {
      const timer = setTimeout(callback, milliseconds);
      return () => clearTimeout(timer);
    });
  const now = environment.now ?? (() => Date.now());
  const listeners = new Set<() => void>();
  let current: ADBShareState = { phase: "idle" };
  // Each start and stop begins a new run; whatever an earlier one still has
  // in flight finds out and gives up.
  let run = 0;
  let target: Target | undefined;
  let live: Live | undefined;
  let retry: (() => void) | undefined;
  let attempts = 0;
  // The phone came back while a retry was already under way.
  let returned = false;
  // An earlier open or close must finish before another attempt claims USB.
  let usbReady: Promise<void> = Promise.resolve();

  function set(next: ADBShareState) {
    current = next;
    for (const listener of [...listeners]) listener();
  }

  function fail(code: string, detail?: string) {
    const device = target?.name;
    target = undefined;
    attempts = 0;
    set({ phase: "failed", device, code, detail });
  }

  function wait(active: number, code: string) {
    if (active !== run || !target) return;
    set({ phase: "waiting", device: target.name, code });
    let delay = 0;
    if (returned) returned = false;
    else {
      delay = Math.min(retryLimit, retryBase * 2 ** Math.min(attempts++, 5));
      delay += Math.random() * delay * 0.2;
    }
    retry = schedule(() => {
      retry = undefined;
      void resume(active);
    }, delay);
  }

  async function resume(active: number) {
    if (active !== run || !target) return;
    // The stream cannot tell a host that refuses from one that is unreachable.
    let availability: ADBShareAvailability;
    try {
      availability = await environment.availability();
    } catch {
      return wait(active, "network");
    }
    if (active !== run || !target) return;
    if (!availability.available)
      return fail(availability.reason || "unavailable");
    let devices: USBDeviceLike[];
    try {
      devices = await environment.usb.getDevices();
    } catch {
      return wait(active, "device_gone");
    }
    if (active !== run || !target) return;
    const wanted = target;
    const matches = devices.filter((candidate) =>
      sameSerial(wanted, candidate),
    );
    if (matches.length > 1) wanted.ambiguous = true;
    if (devices.includes(wanted.selected) && hasADBInterface(wanted.selected))
      return attempt(active, wanted.selected);
    if (!wanted.serial || wanted.ambiguous) return fail("reselect_device");
    if (!matches[0] || !hasADBInterface(matches[0]))
      return wait(active, "device_gone");
    await attempt(active, matches[0]);
  }

  function attempt(active: number, device: USBDeviceLike) {
    if (active !== run || !target) return Promise.resolve();
    const pending = usbReady.then(() => open(active, device));
    usbReady = pending;
    set({ phase: "connecting", device: target.name });
    return pending;
  }

  async function open(active: number, device: USBDeviceLike) {
    if (active !== run || !target) return;
    let link: ADBLink;
    try {
      link = await openADBLink(device);
    } catch (error) {
      if (active !== run || !target) return;
      const failure = error instanceof ADBLinkError ? error.failure : "busy";
      if (failure === "gone") return lostPhone(active);
      return fail(
        failure === "no_interface" ? "no_interface" : "usb_busy",
        error instanceof Error ? error.message : undefined,
      );
    }
    if (active !== run || !target) {
      await link.close();
      return;
    }
    let socket: WebSocket;
    try {
      socket = environment.socket();
    } catch {
      await link.close();
      return wait(active, "network");
    }
    socket.binaryType = "arraybuffer";
    const session: Live = {
      run: active,
      device,
      link,
      socket,
      ended: false,
      fromHost: new ByteQueue(),
      writing: false,
    };
    live = session;
    returned = false;
    target.selected = device;
    const key = target.key;
    listen(session);
    socket.onopen = () => {
      if (session.ended) return;
      socket.send(
        JSON.stringify({
          type: "attach",
          csrf: environment.csrf(),
          transport: "usb",
          device: key,
        }),
      );
      void readPhone(session);
    };
    socket.onmessage = (event: MessageEvent) => {
      if (session.ended) return;
      listen(session);
      if (typeof event.data === "string") control(session, event.data);
      else if (event.data instanceof ArrayBuffer) {
        session.fromHost.push(new Uint8Array(event.data));
        void writePhone(session);
      }
    };
    socket.onerror = () => end(session, { by: "network" });
    socket.onclose = () => end(session, { by: "network" });
  }

  // Ends the session if the host goes quiet.
  function listen(session: Live) {
    session.quiet?.();
    session.quiet = schedule(
      () => end(session, { by: "network" }),
      silenceLimit,
    );
  }

  function control(session: Live, text: string) {
    let message: {
      type?: string;
      serial?: string;
      state?: string;
      code?: string;
      message?: string;
    };
    try {
      message = JSON.parse(text);
    } catch {
      return;
    }
    if (!message || typeof message !== "object") return;
    const device = target?.name;
    switch (message.type) {
      case "ready":
        if (typeof message.serial === "string") session.serial = message.serial;
        break;
      case "state":
        if (message.state === "unauthorized")
          set({ phase: "unauthorized", device, serial: session.serial });
        else if (message.state === "online") {
          session.onlineAt = now();
          set({ phase: "online", device, serial: session.serial });
        }
        break;
      case "error":
        end(session, {
          by: "host",
          code: typeof message.code === "string" ? message.code : "internal",
          detail:
            typeof message.message === "string" ? message.message : undefined,
        });
        break;
    }
  }

  async function readPhone(session: Live) {
    try {
      while (!session.ended) {
        const packet = await session.link.read();
        while (
          !session.ended &&
          session.socket.bufferedAmount > uploadHighWater
        )
          await new Promise<void>((resolve) => schedule(resolve, 20));
        if (session.ended) return;
        session.socket.send(packet);
      }
    } catch {
      end(session, { by: "phone" });
    }
  }

  async function writePhone(session: Live) {
    if (session.writing) return;
    session.writing = true;
    try {
      while (!session.ended) {
        if (!session.header) {
          const header = session.fromHost.take(adbHeaderSize);
          if (!header) return;
          const length = adbPayloadLength(header);
          if (length === undefined || length > adbMaxPayload) {
            // adb never sends this; relaying it would only confuse the phone.
            end(session, { by: "host", code: "protocol" });
            return;
          }
          session.header = header;
        }
        const payload = session.fromHost.take(
          adbPayloadLength(session.header)!,
        );
        if (!payload) return;
        const header = session.header;
        session.header = undefined;
        await session.link.write(header, payload);
      }
    } catch {
      end(session, { by: "phone" });
    } finally {
      session.writing = false;
    }
  }

  function close(session: Live) {
    session.ended = true;
    session.quiet?.();
    if (live === session) live = undefined;
    session.socket.onopen = null;
    session.socket.onmessage = null;
    session.socket.onerror = null;
    session.socket.onclose = null;
    try {
      session.socket.close();
    } catch {
      // Already closed.
    }
    usbReady = session.link.close();
  }

  function lostPhone(active: number) {
    if (active !== run || !target) return;
    if (!target.serial || target.ambiguous) return fail("reselect_device");
    wait(active, "device_gone");
  }

  function end(session: Live, ending: Ending) {
    if (session.ended) return;
    close(session);
    if (session.run !== run) return;
    if (
      session.onlineAt !== undefined &&
      now() - session.onlineAt >= steadyAfter
    )
      attempts = 0;
    if (ending.by === "host" && !resumable.has(ending.code))
      return fail(ending.code, ending.detail);
    if (ending.by === "phone") return lostPhone(session.run);
    wait(session.run, ending.by === "host" ? ending.code : "network");
  }

  const onDisconnect = (event: { device: USBDeviceLike }) => {
    if (live && live.device === event.device) end(live, { by: "phone" });
  };
  const onConnect = (event: { device: USBDeviceLike }) => {
    if (!target) return;
    if (target.selected !== event.device) {
      if (!sameSerial(target, event.device)) return;
      if (live) target.ambiguous = true;
    }
    if (current.phase !== "waiting") return;
    if (!retry) {
      returned = true;
      return;
    }
    retry();
    retry = undefined;
    attempts = 0;
    void resume(run);
  };
  environment.usb.addEventListener("connect", onConnect);
  environment.usb.addEventListener("disconnect", onDisconnect);

  function stop() {
    run++;
    retry?.();
    retry = undefined;
    if (live) close(live);
    target = undefined;
    attempts = 0;
    returned = false;
    if (current.phase !== "idle") set({ phase: "idle" });
  }

  return {
    state: () => current,
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    async start() {
      if (current.phase !== "idle" && current.phase !== "failed") return;
      const active = ++run;
      set({ phase: "choosing" });
      let device: USBDeviceLike;
      try {
        device = await environment.usb.requestDevice({
          filters: [adbInterface],
        });
      } catch (error) {
        if (active !== run) return;
        // Closing the browser's device list is not a failure.
        if (error instanceof Error && error.name === "NotFoundError")
          set({ phase: "idle" });
        else
          set({
            phase: "failed",
            code: "usb",
            detail: error instanceof Error ? error.message : undefined,
          });
        return;
      }
      if (active !== run) return;
      const picked = identify(device);
      target = picked;
      attempts = 0;
      returned = false;
      if (picked.serial) {
        try {
          picked.ambiguous = (await environment.usb.getDevices()).some(
            (candidate) =>
              candidate !== device && sameSerial(picked, candidate),
          );
        } catch {
          // The user's explicit choice can still open even if enumeration fails.
        }
        if (picked.ambiguous)
          picked.key = `usb-selection-${crypto.randomUUID()}`;
      }
      await attempt(active, device);
    },
    stop,
    dispose() {
      stop();
      environment.usb.removeEventListener("connect", onConnect);
      environment.usb.removeEventListener("disconnect", onDisconnect);
      listeners.clear();
    },
  };
}
