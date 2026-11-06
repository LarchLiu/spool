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
// complete, literal read/list commands get a more specific label.
function shellWords(source: string, boundary?: (end: number, separator: string) => void): string[][] | undefined {
  const commands: string[][] = [],
    words: string[] = [];
  let word = "",
    quote = "",
    started = false;
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
      if (words.length) commands.push(words.splice(0));
      const doubled = char === source[i + 1] && (char === "|" || char === "&");
      boundary?.(i, doubled ? char + char : char);
      if (doubled) i++;
    } else if (/\s/.test(char)) flush();
    else {
      word += char;
      started = true;
    }
  }
  if (quote) return;
  flush();
  if (words.length) commands.push(words);
  boundary?.(source.length, "");
  return commands;
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

// Derived rows describe input operations only. A pipeline has one combined
// output, so keep its stages together and never offer per-operation details.
export function shellOperations(source: string, depth = 0): ToolOperation[] | undefined {
  if (depth > 4 || source.length > 65536) return;
  const groups: string[] = [];
  let start = 0, stageStart = 0, pipeline = false, required = false, valid = true;
  const commands = shellWords(source, (end, separator) => {
    const stage = source.slice(stageStart, end).trim();
    // Permit a newline after a pipe/conditional, but reject incomplete stages.
    if (!stage && required && separator === "\n") return;
    if (!stage && (required || ["|", "||", "&", "&&"].includes(separator))) valid = false;
    required = ["|", "||", "&&"].includes(separator);
    stageStart = end + separator.length;
    if (separator === "|") { pipeline = true; return; }
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
  if (groups.length < 2 && !pipeline) return;
  const operations: ToolOperation[] = [];
  for (const group of groups) {
    const groupCommands = shellWords(group);
    if (!groupCommands?.length) return;
    const script = groupCommands?.length === 1 ? shellScript(groupCommands[0]) : undefined;
    const nested = script === undefined ? undefined : shellOperations(script, depth + 1);
    if (nested) operations.push(...nested);
    else {
      const kind = shellCommandKind(group, depth);
      const path = operationPath(groupCommands[0], kind);
      // Bound metadata after classification; a truncated command is never parsed.
      if (new TextEncoder().encode(group).length > 512 || new TextEncoder().encode(path).length > 1024) return;
      operations.push({ kind: kind as ToolOperation["kind"], command: group, ...(path ? { path } : {}) });
    }
    if (operations.length > 64) return;
  }
  return operations;
}

function operationPath(words: string[], kind: ActivityKind): string {
  if (kind !== "read" && kind !== "list" && kind !== "search") return "";
  const { command, args } = commandArgs(words);
  if (command === "sed") {
    const files = sedPrintFiles(args);
    return files?.length === 1 ? files[0] : "";
  }
  const search = /^(rg|ripgrep|grep|egrep|fgrep)$/.test(command);
  let pattern = kind === "list", options = true;
  const paths: string[] = [];
  for (let i = 0; i < args.length; i++) {
    const arg = args[i];
    if (options && arg === "--") { options = false; continue; }
    if (options && arg.startsWith("-")) {
      if (search) {
        if (/^(?:-[ef]|--(?:regexp|file))(?:=|$)/.test(arg) || /^-[ef].+/.test(arg)) pattern = true;
        if (/^(?:-[efgtTmjErABC]|--(?:regexp|file|glob|iglob|type|type-not|ignore-file|pre|pre-glob|encoding|max-count|max-depth|threads|replace|sort|sortr|color|colors|context|after-context|before-context|path-separator|hostname-bin|hyperlink-format|include|exclude|exclude-dir))$/.test(arg)) i++;
      } else if (/^(head|tail)$/.test(command) && /^(?:-[nc]|--(?:lines|bytes))$/.test(arg)) i++;
      else if (!/^(cat|head|tail)$/.test(command)) return "";
      continue;
    }
    if (search && !pattern) { pattern = true; continue; }
    if (arg === "-") continue;
    paths.push(arg);
  }
  return paths.length === 1 ? paths[0] : "";
}

// Recognize print-only sed scripts. Even without -i, sed's e/w commands and
// substitution flags can execute programs or write files.
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

export function shellSearchPath(source: string, depth = 0): string {
  if (depth > 4 || source.length > 65536) return "";
  const commands = shellWords(source);
  if (commands?.length !== 1) return "";
  const script = shellScript(commands[0]);
  return script === undefined ? operationPath(commands[0], "search") : shellSearchPath(script, depth + 1);
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
      if (
        /^(?:-[efgtTmjErABC]|--(?:regexp|file|glob|iglob|type|type-not|ignore-file|pre|pre-glob|encoding|max-count|max-depth|threads|replace|sort|sortr|color|colors|context|after-context|before-context|path-separator|hostname-bin|hyperlink-format))$/.test(
          arg,
        )
      )
        i++;
    }
    return "read";
  }
  if (/^(grep|egrep|fgrep)$/.test(command)) return "read";
  if (command === "ls" || command === "tree") return "list";
  if (command === "find")
    return args.some((arg) =>
      /^-(delete|exec|execdir|ok|okdir|fprint|fprint0|fprintf|fls)$/.test(arg),
    )
      ? "shell"
      : "list";
  if (/^(cat|head|tail|less|more)$/.test(command)) return "read";
  if (command === "sed" && sedPrintFiles(args)) return "search";
  return "shell";
}

export function shellCommandKind(source: string, depth = 0): ActivityKind {
  if (depth > 4 || source.length > 65536) return "shell";
  const commands = shellWords(source);
  if (!commands?.length || commands.length > 64) return "shell";
  const kinds = commands.map((words) => commandKind(words, depth));
  return kinds.every((kind) => kind === kinds[0]) ? kinds[0] : "shell";
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
