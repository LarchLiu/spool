import type { ToolOperation } from "./types";

export type ActivityKind =
  | "read"
  | "write"
  | "create"
  | "delete"
  | "search"
  | "list"
  | "web"
  | "shell"
  | "thinking"
  | "message"
  | "user"
  | "image"
  | "other";

export function toolNameKind(name: string): ActivityKind {
  const tool = name.trim().toLowerCase().split(/\.|__/).pop() || "";
  const compact = tool.replace(/[_-]/g, "");
  if (/^(reason|reasoning|think|thinking)$/.test(compact)) return "thinking";
  if (
    /^(applypatch|patch|filechange|edit|editfile|multiedit|notebookedit|replace)$/.test(
      compact,
    )
  )
    return "write";
  if (/^(write|writefile|createfile)$/.test(compact)) return "create";
  if (/^(deletefile|removefile|unlink)$/.test(compact)) return "delete";
  if (/^(read|readfile|getfile|openfile|viewfile|notebookread)$/.test(compact))
    return "read";
  if (
    /^(glob|listfiles|listdirectory|directorylisting|readdir|ls|find)$/.test(
      compact,
    )
  )
    return "list";
  if (/^(grep|ripgrep|rg|searchfiles|searchcode|findinfiles)$/.test(compact))
    return "search";
  if (
    /^(websearch|webfetch|searchweb|browser|internet)$/.test(compact) ||
    tool.startsWith("browser_")
  )
    return "web";
  if (
    /^(shell|bash|zsh|sh|command|terminal|exec|execcommand|execute|executecommand|runcommand|shellcommand|commandexecution|writestdin|bashoutput|killshell|killbash)$/.test(
      compact,
    )
  )
    return "shell";
  return "other";
}

// This is a conservative display classifier, not a shell interpreter. Only
// complete, literal commands get a more specific label. Kinds follow Codex's
// own reading of a command (a content search is a search, a printed range is
// a read), so both providers label the same command alike.
type ShellCommands = { commands: string[][]; piped: boolean[] };

function shellWords(source: string, boundary?: (end: number, separator: string) => void): string[][] | undefined {
  return shellParse(source, boundary)?.commands;
}

// Also reports which commands read the previous command's output.
function shellParse(source: string, boundary?: (end: number, separator: string) => void): ShellCommands | undefined {
  const commands: string[][] = [],
    piped: boolean[] = [],
    words: string[] = [];
  let word = "",
    quote = "",
    started = false,
    afterPipe = false;
  const flush = () => {
    if (started) words.push(word);
    word = "";
    started = false;
  };
  for (let i = 0; i < source.length; i++) {
    const char = source[i];
    if (char === "\\" && quote !== "'") {
      if (++i === source.length) return;
      if (source[i] !== "\n") {
        word += source[i];
        started = true;
      }
    } else if (char === quote) quote = "";
    else if (char === "'" || char === '"') {
      if (quote) word += char;
      else {
        quote = char;
        started = true;
      }
    } else if (quote === "'") word += char;
    else if ("$`<>(){}".includes(char)) return;
    else if (quote) word += char;
    else if (char === "#" && !started) return;
    else if (";|&\n".includes(char)) {
      flush();
      const stage = words.length > 0;
      if (stage) {
        commands.push(words.splice(0));
        piped.push(afterPipe);
      }
      const doubled = char === source[i + 1] && (char === "|" || char === "&");
      const separator = doubled ? char + char : char;
      // A newline may separate a pipe from its next stage.
      if (stage || separator !== "\n" || !afterPipe) afterPipe = separator === "|";
      boundary?.(i, separator);
      if (doubled) i++;
    } else if (/\s/.test(char)) flush();
    else {
      word += char;
      started = true;
    }
  }
  if (quote) return;
  flush();
  if (words.length) {
    commands.push(words);
    piped.push(afterPipe);
  }
  boundary?.(source.length, "");
  return { commands, piped };
}

function commandArgs(words: string[]) {
  let at = 0;
  while (/^[A-Za-z_][A-Za-z0-9_]*=/.test(words[at] || "")) at++;
  let command = (words[at++] || "").split("/").pop() || "";
  if (command === "env") {
    if (words[at] === "--") at++;
    while (/^[A-Za-z_][A-Za-z0-9_]*=/.test(words[at] || "")) at++;
    command = (words[at++] || "").split("/").pop() || "";
  }
  return { command, args: words.slice(at) };
}

function shellScript(words: string[]): string | undefined {
  const { command, args } = commandArgs(words);
  if (!/^(sh|bash|zsh|dash|ksh|fish)$/.test(command)) return;
  const flag = args.findIndex((arg) => /^-[lc]*c[lc]*$/.test(arg));
  if (flag >= 0 && args.slice(0, flag).every((arg) => /^-l+$/.test(arg)) && args.length === flag + 2)
    return args[flag + 1];
}

// Derived rows describe input operations only. Each sequential step becomes
// one row; a pipeline has one combined output, so its stages stay together.
export function shellOperations(source: string, depth = 0): ToolOperation[] | undefined {
  if (depth > 4 || source.length > 65536) return;
  const groups: string[] = [];
  let start = 0, stageStart = 0, required = false, valid = true;
  const commands = shellWords(source, (end, separator) => {
    const stage = source.slice(stageStart, end).trim();
    // Permit a newline after a pipe/conditional, but reject incomplete stages.
    if (!stage && required && separator === "\n") return;
    if (!stage && (required || ["|", "||", "&", "&&"].includes(separator))) valid = false;
    required = ["|", "||", "&&"].includes(separator);
    stageStart = end + separator.length;
    if (separator === "|") return;
    const group = source.slice(start, end).trim();
    if (group) groups.push(group);
    start = end + separator.length;
  });
  if (!valid || !commands?.length || commands.length > 64) return;
  if (commands.some(words => /^(if|then|elif|else|fi|for|while|until|do|done|case|esac|select|function|!|time)$/.test(commandArgs(words).command))) return;
  if (commands.length === 1) {
    const script = shellScript(commands[0]);
    if (script !== undefined) return shellOperations(script, depth + 1);
  }
  // A lone pipeline did one thing; it is summarized as a single call.
  if (groups.length < 2) return;
  const operations: ToolOperation[] = [];
  const bytes = (value: string) => new TextEncoder().encode(value).length;
  for (const group of groups) {
    const groupCommands = shellWords(group);
    if (!groupCommands?.length) return;
    const script = groupCommands?.length === 1 ? shellScript(groupCommands[0]) : undefined;
    const nested = script === undefined ? undefined : shellOperations(script, depth + 1);
    if (nested) operations.push(...nested);
    else {
      const kind = shellCommandKind(group, depth);
      const { path, query } = operationTarget(groupCommands[0], kind);
      // Bound metadata after classification; a truncated command is never parsed.
      if (bytes(group) > 512 || bytes(path) > 1024 || bytes(query) > 1024) return;
      operations.push({ kind: kind as ToolOperation["kind"], command: group, ...(path ? { path } : {}), ...(query ? { query } : {}) });
    }
    if (operations.length > 64) return;
  }
  return operations;
}

const rgValueFlag = /^(?:-[efgtTmjErABC]|--(?:regexp|file|glob|iglob|type|type-not|ignore-file|pre|pre-glob|encoding|max-count|max-depth|threads|replace|sort|sortr|color|colors|context|after-context|before-context|path-separator|hostname-bin|hyperlink-format))$/;
// grep spells its options differently from rg: -r and -E take no value there.
const grepValueFlag = /^(?:-[efmABCdD]|--(?:regexp|file|max-count|context|after-context|before-context|include|exclude|exclude-dir|exclude-from|binary-files|devices|directories|label))$/;
const headValueFlag = /^(?:-[nc]|--(?:lines|bytes))$/;
const lsPlainFlag = /^-[1aAbBcCdfFgGhHiklLmnopqrRsStuUvxX]+$/;
const findNameFlag = /^-i?(?:name|path|regex)$/;
const searchCommand = /^(rg|ripgrep|grep|egrep|fgrep)$/;

// An option of this search command whose value is the next argument.
const searchValueFlag = (command: string, arg: string) => (command === "rg" || command === "ripgrep" ? rgValueFlag : grepValueFlag).test(arg);

// A grep-like command's pattern and path operands. `patterned` is also true
// for a pattern the command reads from a file.
function searchOperands(command: string, args: string[]) {
  let query = "", patterned = false, options = true;
  const paths: string[] = [];
  for (let i = 0; i < args.length; i++) {
    const arg = args[i];
    if (options && arg === "--") { options = false; continue; }
    if (options && arg.startsWith("-") && arg !== "-") {
      let value = "", pattern = true;
      if (arg === "-e" || arg === "--regexp") value = args[i + 1] ?? "";
      else if (arg === "-f" || arg === "--file" || arg.startsWith("--file=") || arg.startsWith("-f")) value = "";
      else if (arg.startsWith("--regexp=")) value = arg.slice(9);
      else if (arg.startsWith("-e")) value = arg.slice(2);
      else pattern = false;
      if (pattern && !patterned) { query = value; patterned = true; }
      if (searchValueFlag(command, arg)) i++;
      continue;
    }
    if (!patterned) { query = arg; patterned = true; continue; }
    if (arg !== "-") paths.push(arg);
  }
  return { query, paths, patterned };
}

// The single path, and for a search its pattern, named by a read, listing or
// search command. Anything less certain stays empty.
function operationTarget(words: string[], kind: ActivityKind): { path: string; query: string } {
  if (kind !== "read" && kind !== "list" && kind !== "search") return { path: "", query: "" };
  const one = (paths: string[] | undefined) => (paths?.length === 1 ? paths[0] : "");
  const { command, args } = commandArgs(words);
  if (command === "sed") return { path: one(sedPrintFiles(args)), query: "" };
  if (command === "find") {
    const paths: string[] = [];
    let query = "";
    for (let i = 0; i < args.length; i++) {
      if (args[i].startsWith("-") || args[i] === "!") {
        for (let j = i; j + 1 < args.length && kind === "search"; j++)
          if (findNameFlag.test(args[j])) { query = args[j + 1]; break; }
        break;
      }
      paths.push(args[i]);
    }
    return { path: one(paths), query };
  }
  if (searchCommand.test(command) && kind === "search") {
    const { query, paths } = searchOperands(command, args);
    return { path: one(paths), query };
  }
  const paths: string[] = [];
  let options = true;
  for (let i = 0; i < args.length; i++) {
    const arg = args[i];
    if (options && arg === "--") { options = false; continue; }
    if (options && arg.startsWith("-") && arg !== "-") {
      if (searchCommand.test(command)) { if (searchValueFlag(command, arg)) i++; }
      else if (/^(head|tail)$/.test(command) && headValueFlag.test(arg)) i++;
      else if (/^(cat|head|tail|nl)$/.test(command)) { /* flags without values */ }
      else if (command === "ls" && lsPlainFlag.test(arg)) { /* flags without values */ }
      else return { path: "", query: "" };
      continue;
    }
    if (arg !== "-") paths.push(arg);
  }
  return { path: one(paths), query: "" };
}

// A pipeline stage that only narrows or formats the output of the stage
// before it, and so does not change what the pipeline did.
function pipeFilter(words: string[]): boolean {
  const { command, args } = commandArgs(words);
  const operands = (valueFlag?: RegExp) => {
    let count = 0;
    for (let i = 0; i < args.length; i++) {
      if (args[i].startsWith("-") && args[i] !== "-") { if (valueFlag?.test(args[i])) i++; continue; }
      count++;
    }
    return count;
  };
  switch (command) {
    case "wc": case "tr": case "cut": case "column":
      return true;
    case "sort":
      return !args.some(arg => arg.startsWith("-o") || arg.startsWith("--output"));
    case "uniq":
      return operands() === 0;
    case "head": case "tail":
      return operands(headValueFlag) === 0;
    case "rg": case "ripgrep": case "grep": case "egrep": case "fgrep": {
      if (args.includes("--files")) return false;
      const { paths, patterned } = searchOperands(command, args);
      return patterned && paths.length === 0;
    }
    case "sed":
      return sedPrintFiles(args)?.length === 0;
  }
  return false;
}

// Recognize print-only sed scripts, which read a file. Even without -i, sed's
// e/w commands and substitution flags can execute programs or write files.
function sedPrintFiles(args: string[]): string[] | undefined {
  const scripts: string[] = [], files: string[] = [];
  let options = true;
  for (let i = 0; i < args.length; i++) {
    const arg = args[i];
    if (options && arg === "--") { options = false; continue; }
    if (options && arg.startsWith("-")) {
      if (/^-[nEr]+$/.test(arg) || /^(--quiet|--silent|--regexp-extended)$/.test(arg)) continue;
      if (arg === "-e" || arg === "--expression") {
        if (++i === args.length) return;
        scripts.push(args[i]);
      } else if (arg.startsWith("--expression=")) scripts.push(arg.slice(13));
      else if (arg.startsWith("-e") && arg.length > 2) scripts.push(arg.slice(2));
      else return;
    } else if (!scripts.length) scripts.push(arg);
    else files.push(arg);
  }
  const print = /^\s*(?:(?:[0-9]+|\$)\s*(?:,\s*(?:[0-9]+|\$)\s*)?)?p\s*$/;
  return scripts.length && scripts.every(script => {
    const items = script.split(/[;\n]/).filter(item => item.trim());
    return items.length > 0 && items.every(item => print.test(item));
  }) ? files : undefined;
}

// The path and pattern of a command that does one thing.
export function shellTarget(source: string, depth = 0): { path: string; query: string } {
  const none = { path: "", query: "" };
  if (depth > 4 || source.length > 65536) return none;
  const parsed = shellParse(source);
  if (!parsed?.commands.length) return none;
  const { commands, piped } = parsed;
  if (commands.length === 1) {
    const script = shellScript(commands[0]);
    if (script !== undefined) return shellTarget(script, depth + 1);
  }
  for (let i = 1; i < commands.length; i++) if (!piped[i] || !pipeFilter(commands[i])) return none;
  return operationTarget(commands[0], commandKind(commands[0], depth));
}

function commandKind(words: string[], depth: number): ActivityKind {
  const { command, args } = commandArgs(words);
  if (/^(sh|bash|zsh|dash|ksh|fish)$/.test(command)) {
    const flag = args.findIndex((arg) => /^-[lc]*c[lc]*$/.test(arg));
    if (
      flag >= 0 &&
      args.slice(0, flag).every((arg) => /^-l+$/.test(arg)) &&
      args.length === flag + 2
    )
      return shellCommandKind(args[flag + 1], depth + 1);
    return "shell";
  }
  if (command === "rg" || command === "ripgrep") {
    for (let i = 0; i < args.length; i++) {
      const arg = args[i];
      if (arg === "--") break;
      if (arg === "--files") return "list";
      if (rgValueFlag.test(arg)) i++;
    }
    return "search";
  }
  if (/^(grep|egrep|fgrep)$/.test(command)) return "search";
  if (command === "ls" || command === "tree") return "list";
  if (command === "find") {
    if (args.some((arg) => /^-(delete|exec|execdir|ok|okdir|fprint|fprint0|fprintf|fls)$/.test(arg))) return "shell";
    return args.some((arg) => findNameFlag.test(arg)) ? "search" : "list";
  }
  if (/^(cat|head|tail|less|more|nl)$/.test(command)) return "read";
  if (command === "sed" && sedPrintFiles(args)) return "read";
  return "shell";
}

export function shellCommandKind(source: string, depth = 0): ActivityKind {
  if (depth > 4 || source.length > 65536) return "shell";
  const parsed = shellParse(source);
  if (!parsed?.commands.length || parsed.commands.length > 64) return "shell";
  const { commands, piped } = parsed;
  const kind = commandKind(commands[0], depth);
  for (let i = 1; i < commands.length; i++) {
    if (piped[i] && pipeFilter(commands[i])) continue;
    if (commandKind(commands[i], depth) !== kind) return "shell";
  }
  return kind;
}

export function toolCommand(
  text: string,
  data?: Record<string, unknown>,
): string {
  if (!data) return text.trimStart().startsWith("{") ? "" : text;
  for (const key of ["cmd", "command"]) {
    const value = data[key];
    if (typeof value === "string") return value;
    // Quote argv to preserve shell -c argument boundaries without executing it.
    if (Array.isArray(value) && value.every((arg) => typeof arg === "string"))
      return value.map((arg) => `'${arg.replaceAll("'", "'\\''")}'`).join(" ");
  }
  return "";
}
