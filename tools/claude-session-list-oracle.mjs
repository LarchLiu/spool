// Optional compatibility oracle for Go tests. The SDK and all provider homes
// must live in the owning tools/run.sh run; this is never shipped with Spool.
import { pathToFileURL } from "node:url";

const [sdkPath, optionsJSON] = process.argv.slice(2);
const { listSessions } = await import(pathToFileURL(sdkPath).href);
console.log(JSON.stringify(await listSessions(JSON.parse(optionsJSON))));
