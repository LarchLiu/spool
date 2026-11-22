// Decode locally; pairing images and their contents never leave this page.
export async function readPairingQRCode(file: File): Promise<string> {
  const source = URL.createObjectURL(file);
  try {
    const image = new Image();
    image.src = source;
    try {
      await image.decode();
    } catch {
      throw new Error("Could not read this image.");
    }
    const scale = Math.min(1, 2048 / Math.max(image.width, image.height));
    const canvas = document.createElement("canvas");
    canvas.width = Math.max(1, Math.round(image.width * scale));
    canvas.height = Math.max(1, Math.round(image.height * scale));
    const context = canvas.getContext("2d", { willReadFrequently: true });
    if (!context) throw new Error("Could not read this image.");
    context.drawImage(image, 0, 0, canvas.width, canvas.height);
    const pixels = context.getImageData(0, 0, canvas.width, canvas.height);
    const { default: decode } = await import("jsqr");
    const code = decode(pixels.data, pixels.width, pixels.height)?.data.trim();
    if (!code?.startsWith("spool-pair:"))
      throw new Error("No Spool pairing QR code was found in this image.");
    return code;
  } finally {
    URL.revokeObjectURL(source);
  }
}
