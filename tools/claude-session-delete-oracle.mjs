// Optional SDK parity oracle; its caller supplies an isolated provider home.
import { pathToFileURL } from "node:url";

const [sdkPath, id] = process.argv.slice(2);
const sdk = await import(pathToFileURL(sdkPath).href);
await sdk.deleteSession(id);
