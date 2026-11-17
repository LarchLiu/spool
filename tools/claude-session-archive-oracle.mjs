// Optional parity oracle, executed only with an isolated provider home.
import { pathToFileURL } from "node:url";

const [sdkPath, action, id] = process.argv.slice(2);
const sdk = await import(pathToFileURL(sdkPath).href);
if (action === "archive") await sdk.archiveSession(id);
else if (action === "unarchive") await sdk.unarchiveSession(id);
else if (action !== "list") throw new Error("Invalid oracle action");
console.log(JSON.stringify(await sdk.listSessions()));
