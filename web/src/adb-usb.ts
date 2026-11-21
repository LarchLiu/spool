// The phone's side of a shared device: its USB debugging interface, driven
// through WebUSB. This module frames ADB packets and nothing more. Their
// contents, the handshake and the phone's authorization prompt all stay
// between the phone and the adb server on the Spool host.

// Only the parts of WebUSB used here, so the project needs no ambient types.
export type USBEndpointInfo = {
  endpointNumber: number;
  direction: "in" | "out";
  type: string;
  packetSize: number;
};
export type USBAlternateInfo = {
  alternateSetting: number;
  interfaceClass: number;
  interfaceSubclass: number;
  interfaceProtocol: number;
  endpoints: USBEndpointInfo[];
};
export type USBInterfaceInfo = {
  interfaceNumber: number;
  alternate?: USBAlternateInfo | null;
  alternates: USBAlternateInfo[];
};
export type USBConfigurationInfo = {
  configurationValue: number;
  interfaces: USBInterfaceInfo[];
};
export type USBDeviceLike = {
  vendorId: number;
  productId: number;
  serialNumber?: string | null;
  productName?: string | null;
  manufacturerName?: string | null;
  configuration?: USBConfigurationInfo | null;
  configurations: USBConfigurationInfo[];
  open(): Promise<void>;
  close(): Promise<void>;
  selectConfiguration(value: number): Promise<void>;
  claimInterface(interfaceNumber: number): Promise<void>;
  selectAlternateInterface(
    interfaceNumber: number,
    alternateSetting: number,
  ): Promise<void>;
  transferIn(
    endpointNumber: number,
    length: number,
  ): Promise<{ data?: DataView | null; status?: string }>;
  transferOut(
    endpointNumber: number,
    data: Uint8Array<ArrayBuffer>,
  ): Promise<{ status?: string }>;
  clearHalt(direction: "in" | "out", endpointNumber: number): Promise<void>;
};
export type USBLike = {
  requestDevice(options: {
    filters: {
      classCode: number;
      subclassCode: number;
      protocolCode: number;
    }[];
  }): Promise<USBDeviceLike>;
  getDevices(): Promise<USBDeviceLike[]>;
  addEventListener(
    type: "connect" | "disconnect",
    listener: (event: { device: USBDeviceLike }) => void,
  ): void;
  removeEventListener(
    type: "connect" | "disconnect",
    listener: (event: { device: USBDeviceLike }) => void,
  ): void;
};

// The interface every Android device exposes while USB debugging is on.
export const adbInterface = {
  classCode: 0xff,
  subclassCode: 0x42,
  protocolCode: 0x01,
};

export const adbHeaderSize = 24;
// The largest payload either side of the ADB protocol may send.
export const adbMaxPayload = 1 << 20;

export type ADBLinkFailure =
  // The device has no USB debugging interface (debugging is off).
  | "no_interface"
  // The interface cannot be opened: another program on this computer holds
  // it, or the operating system refuses access.
  | "busy"
  // The device is no longer connected.
  | "gone"
  // The device or the cable failed, or what arrived was not ADB.
  | "transfer";

export class ADBLinkError extends Error {
  constructor(
    readonly failure: ADBLinkFailure,
    detail = "",
  ) {
    super(detail || failure);
    this.name = "ADBLinkError";
  }
}

// A packet header as it travels: six little-endian words, the last one the
// complement of the first.
export function adbPayloadLength(header: Uint8Array): number | undefined {
  if (header.byteLength < adbHeaderSize) return undefined;
  const view = new DataView(header.buffer, header.byteOffset, adbHeaderSize);
  if ((view.getUint32(0, true) ^ 0xffffffff) >>> 0 !== view.getUint32(20, true))
    return undefined;
  return view.getUint32(12, true);
}

type Match = {
  configuration: USBConfigurationInfo;
  interfaceNumber: number;
  alternate: USBAlternateInfo;
  input: USBEndpointInfo;
  output: USBEndpointInfo;
};

function findADB(device: USBDeviceLike): Match | undefined {
  for (const configuration of device.configurations)
    for (const candidate of configuration.interfaces)
      for (const alternate of candidate.alternates) {
        if (
          alternate.interfaceClass !== adbInterface.classCode ||
          alternate.interfaceSubclass !== adbInterface.subclassCode ||
          alternate.interfaceProtocol !== adbInterface.protocolCode
        )
          continue;
        const bulk = (direction: "in" | "out") =>
          alternate.endpoints.find(
            (endpoint) =>
              endpoint.type === "bulk" && endpoint.direction === direction,
          );
        const input = bulk("in");
        const output = bulk("out");
        if (input && output && input.packetSize > 0 && output.packetSize > 0)
          return {
            configuration,
            interfaceNumber: candidate.interfaceNumber,
            alternate,
            input,
            output,
          };
      }
}

export function hasADBInterface(device: USBDeviceLike) {
  return findADB(device) !== undefined;
}

function failure(error: unknown, fallback: ADBLinkFailure): ADBLinkError {
  if (error instanceof ADBLinkError) return error;
  const name = error instanceof Error ? error.name : "";
  const detail = error instanceof Error ? error.message : String(error);
  return new ADBLinkError(name === "NotFoundError" ? "gone" : fallback, detail);
}

export type ADBLink = {
  // Resolves with one complete packet from the phone, header and payload.
  read(): Promise<Uint8Array<ArrayBuffer>>;
  // Sends one packet to the phone. Calls must not overlap.
  write(
    header: Uint8Array<ArrayBuffer>,
    payload: Uint8Array<ArrayBuffer>,
  ): Promise<void>;
  // Gives the interface back and ends a read that is still waiting.
  close(): Promise<void>;
};

export async function openADBLink(device: USBDeviceLike): Promise<ADBLink> {
  const match = findADB(device);
  if (!match) throw new ADBLinkError("no_interface");
  try {
    await device.open();
  } catch (error) {
    throw failure(error, "busy");
  }
  try {
    if (
      device.configuration?.configurationValue !==
      match.configuration.configurationValue
    )
      await device.selectConfiguration(match.configuration.configurationValue);
    await device.claimInterface(match.interfaceNumber);
    const selected = device.configuration?.interfaces.find(
      (candidate) => candidate.interfaceNumber === match.interfaceNumber,
    )?.alternate;
    if (
      selected?.alternateSetting !== match.alternate.alternateSetting &&
      (selected || match.alternate.alternateSetting !== 0)
    )
      await device.selectAlternateInterface(
        match.interfaceNumber,
        match.alternate.alternateSetting,
      );
  } catch (error) {
    await device.close().catch(() => {});
    throw failure(error, "busy");
  }

  const input = match.input.endpointNumber;
  const output = match.output.endpointNumber;
  const inputPacket = match.input.packetSize;
  const outputPacket = match.output.packetSize;

  async function receive(length: number): Promise<Uint8Array> {
    for (let stalls = 0; ; stalls++) {
      const result = await device.transferIn(input, length);
      if (result.status === "stall" && stalls < 3) {
        await device.clearHalt("in", input);
        continue;
      }
      if (result.status && result.status !== "ok")
        throw new ADBLinkError("transfer", `USB read ${result.status}`);
      const data = result.data;
      return data
        ? new Uint8Array(data.buffer, data.byteOffset, data.byteLength)
        : new Uint8Array(0);
    }
  }

  async function transfer(data: Uint8Array<ArrayBuffer>) {
    const result = await device.transferOut(output, data);
    if (result.status && result.status !== "ok")
      throw new ADBLinkError("transfer", `USB write ${result.status}`);
  }

  // As adb itself does: a transfer that ends on a full USB packet is not
  // finished for the phone until an empty one follows.
  async function send(data: Uint8Array<ArrayBuffer>) {
    await transfer(data);
    if (data.byteLength % outputPacket === 0)
      await transfer(new Uint8Array(0));
  }

  return {
    async read() {
      try {
        for (;;) {
          // The phone sends each header as its own transfer. Asking for a full
          // USB packet can never overflow, and anything that is not a header,
          // such as the rest of a packet an earlier session left unread, is
          // skipped until the stream lines up again.
          const header = await receive(inputPacket);
          if (header.byteLength !== adbHeaderSize) continue;
          const length = adbPayloadLength(header);
          if (length === undefined) continue;
          if (length > adbMaxPayload)
            throw new ADBLinkError("transfer", "oversized ADB packet");
          const packet = new Uint8Array(adbHeaderSize + length);
          packet.set(header);
          for (let filled = 0; filled < length; ) {
            // As adb does, ask in whole USB packets: a phone that sends more
            // than it announced is then caught here, not by the USB stack.
            const left = length - filled;
            const part = await receive(
              Math.ceil(left / inputPacket) * inputPacket,
            );
            if (part.byteLength > left)
              throw new ADBLinkError("transfer", "oversized ADB payload");
            packet.set(part, adbHeaderSize + filled);
            filled += part.byteLength;
          }
          return packet;
        }
      } catch (error) {
        throw failure(error, "transfer");
      }
    },
    async write(header, payload) {
      try {
        // Header and payload are separate transfers, as the phone expects.
        await send(header);
        if (payload.byteLength > 0) await send(payload);
      } catch (error) {
        throw failure(error, "transfer");
      }
    },
    async close() {
      // Closing releases the interface and rejects a pending transfer.
      await device.close().catch(() => {});
    },
  };
}
