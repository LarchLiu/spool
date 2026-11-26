// Keep successful summaries out of stderr, which some consoles render in red.
export function printStatus(message, success) {
  const stream = success ? process.stdout : process.stderr;
  const forceColor = process.env.FORCE_COLOR;
  const color = process.env.NO_COLOR === undefined &&
    (forceColor !== undefined ? forceColor !== "0" : stream.isTTY && process.env.TERM !== "dumb");
  stream.write((color ? `\x1b[${success ? 32 : 31}m${message}\x1b[0m` : message) + "\n");
}
