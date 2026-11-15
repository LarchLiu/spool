package workspace

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode"

	chatmodel "spool/internal/model"
)

var toolAssignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

func toolNameKind(name string) string {
	name = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), "__", ".")
	name = name[strings.LastIndex(name, ".")+1:]
	compact := strings.NewReplacer("_", "", "-", "").Replace(name)
	switch compact {
	case "reason", "reasoning", "think", "thinking":
		return "thinking"
	case "applypatch", "patch", "filechange", "edit", "editfile", "multiedit", "notebookedit", "replace":
		return "write"
	case "write", "writefile", "createfile":
		return "create"
	case "deletefile", "removefile", "unlink":
		return "delete"
	case "read", "readfile", "getfile", "openfile", "viewfile", "notebookread":
		return "read"
	case "glob", "listfiles", "listdirectory", "directorylisting", "readdir", "ls", "find":
		return "list"
	case "grep", "ripgrep", "rg", "searchfiles", "searchcode", "findinfiles":
		return "search"
	case "websearch", "webfetch", "searchweb", "browser", "internet":
		return "web"
	case "shell", "bash", "zsh", "sh", "command", "terminal", "exec", "execcommand", "execute", "executecommand", "runcommand", "shellcommand", "commandexecution", "writestdin", "bashoutput", "killshell", "killbash":
		return "shell"
	}
	if strings.HasPrefix(name, "browser_") {
		return "web"
	}
	return "other"
}

func toolCommand(text string, data map[string]json.RawMessage) string {
	if data == nil {
		if strings.HasPrefix(strings.TrimSpace(text), "{") {
			return ""
		}
		return text
	}
	for _, key := range []string{"cmd", "command"} {
		var value string
		if json.Unmarshal(data[key], &value) == nil {
			return value
		}
		var args []string
		if json.Unmarshal(data[key], &args) == nil && args != nil {
			for i, arg := range args {
				args[i] = "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
			}
			return strings.Join(args, " ")
		}
	}
	return ""
}

// Only complete literal commands get a specific display kind. Expansion,
// redirection, scripts and mixed activities retain the generic command label.
// Kinds follow Codex's own reading of a command (a content search is a search,
// a printed range is a read), so both providers label the same command alike.
func shellWords(source string, boundaries ...func(int, string)) ([][]string, bool) {
	commands, _, ok := shellParse(source, boundaries...)
	return commands, ok
}

// shellParse also reports which commands read the previous command's output.
func shellParse(source string, boundaries ...func(int, string)) ([][]string, []bool, bool) {
	var commands [][]string
	var piped []bool
	afterPipe := false
	var words []string
	var word strings.Builder
	var quote rune
	started := false
	flush := func() {
		if started {
			words = append(words, word.String())
		}
		word.Reset()
		started = false
	}
	runes := []rune(source)
	for i := 0; i < len(runes); i++ {
		char := runes[i]
		switch {
		case char == '\\' && quote != '\'':
			i++
			if i == len(runes) {
				return nil, nil, false
			}
			if runes[i] != '\n' {
				word.WriteRune(runes[i])
				started = true
			}
		case char == quote:
			quote = 0
		case char == '\'' || char == '"':
			if quote != 0 {
				word.WriteRune(char)
			} else {
				quote, started = char, true
			}
		case quote == '\'':
			word.WriteRune(char)
		case strings.ContainsRune("$`<>(){}", char):
			return nil, nil, false
		case quote != 0:
			word.WriteRune(char)
		case char == '#' && !started:
			return nil, nil, false
		case strings.ContainsRune(";|&\n", char):
			flush()
			stage := len(words) > 0
			if stage {
				commands, piped = append(commands, words), append(piped, afterPipe)
				words = nil
			}
			doubled := i+1 < len(runes) && char == runes[i+1] && (char == '|' || char == '&')
			separator := string(char)
			if doubled {
				separator += string(char)
			}
			// A newline may separate a pipe from its next stage.
			if stage || separator != "\n" || !afterPipe {
				afterPipe = separator == "|"
			}
			for _, boundary := range boundaries {
				boundary(i, separator)
			}
			if doubled {
				i++
			}
		case unicode.IsSpace(char):
			flush()
		default:
			word.WriteRune(char)
			started = true
		}
	}
	if quote != 0 {
		return nil, nil, false
	}
	flush()
	if len(words) > 0 {
		commands, piped = append(commands, words), append(piped, afterPipe)
	}
	for _, boundary := range boundaries {
		boundary(len(runes), "")
	}
	return commands, piped, true
}

func commandArgs(words []string) (string, []string) {
	at := 0
	skipAssignments := func() {
		for at < len(words) && toolAssignment.MatchString(words[at]) {
			at++
		}
	}
	next := func() string {
		if at == len(words) {
			return ""
		}
		word := words[at]
		at++
		return word[strings.LastIndex(word, "/")+1:]
	}
	skipAssignments()
	command := next()
	if command == "env" {
		if at < len(words) && words[at] == "--" {
			at++
		}
		skipAssignments()
		command = next()
	}
	return command, words[at:]
}

func shellScript(words []string) (string, bool) {
	command, args := commandArgs(words)
	switch command {
	case "sh", "bash", "zsh", "dash", "ksh", "fish":
		for i, arg := range args {
			if !shellFlag.MatchString(arg) {
				break
			}
			if strings.ContainsRune(arg, 'c') {
				if len(args) == i+2 {
					return args[i+1], true
				}
				break
			}
		}
	}
	return "", false
}

// Each sequential step becomes one row. A pipeline stays together: the
// provider supplies only its combined output.
func shellOperations(source string, depth int) []chatmodel.ToolOperation {
	if depth > 4 || len(source) > 65536 {
		return nil
	}
	var groups []string
	start, stageStart, required, valid := 0, 0, false, true
	runes := []rune(source)
	commands, ok := shellWords(source, func(end int, separator string) {
		stage := strings.TrimSpace(string(runes[stageStart:end]))
		if stage == "" && required && separator == "\n" {
			return
		}
		if stage == "" && (required || separator == "|" || separator == "||" || separator == "&" || separator == "&&") {
			valid = false
		}
		required = separator == "|" || separator == "||" || separator == "&&"
		stageStart = end + len(separator)
		if separator == "|" {
			return
		}
		if group := strings.TrimSpace(string(runes[start:end])); group != "" {
			groups = append(groups, group)
		}
		start = end + len(separator)
	})
	if !valid || !ok || len(commands) == 0 || len(commands) > 64 {
		return nil
	}
	for _, words := range commands {
		command, _ := commandArgs(words)
		switch command {
		case "if", "then", "elif", "else", "fi", "for", "while", "until", "do", "done", "case", "esac", "select", "function", "!", "time":
			return nil
		}
	}
	if len(commands) == 1 {
		if script, ok := shellScript(commands[0]); ok {
			return shellOperations(script, depth+1)
		}
	}
	// A lone pipeline did one thing; it is summarized as a single call.
	if len(groups) < 2 {
		return nil
	}
	var operations []chatmodel.ToolOperation
	for _, group := range groups {
		groupCommands, _ := shellWords(group)
		if len(groupCommands) == 0 {
			return nil
		}
		var nested []chatmodel.ToolOperation
		if len(groupCommands) == 1 {
			if script, ok := shellScript(groupCommands[0]); ok {
				nested = shellOperations(script, depth+1)
			}
		}
		if len(nested) > 0 {
			operations = append(operations, nested...)
		} else {
			kind := shellCommandKind(group, depth)
			path, query := operationTarget(groupCommands[0], kind)
			if len(group) > 512 || len(path) > 1024 || len(query) > 1024 {
				return nil
			}
			operations = append(operations, chatmodel.ToolOperation{Kind: kind, Command: group, Path: path, Query: query})
		}
		if len(operations) > 64 {
			return nil
		}
	}
	return operations
}

var grepValueFlag = regexp.MustCompile(`^--(?:include|exclude|exclude-dir)$`)
var headValueFlag = regexp.MustCompile(`^(?:-[nc]|--(?:lines|bytes))$`)
var lsPlainFlag = regexp.MustCompile(`^-[1aAbBcCdfFgGhHiklLmnopqrRsStuUvxX]+$`)
var findNameFlag = regexp.MustCompile(`^-i?(?:name|path|regex)$`)

func searchCommand(command string) bool {
	return command == "rg" || command == "ripgrep" || command == "grep" || command == "egrep" || command == "fgrep"
}

// searchOperands reads a grep-like command's pattern and path operands.
// patterned is also true for a pattern the command reads from a file.
func searchOperands(args []string) (query string, paths []string, patterned bool) {
	options := true
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if options && arg == "--" {
			options = false
			continue
		}
		if options && strings.HasPrefix(arg, "-") && arg != "-" {
			value, pattern := "", true
			switch {
			case arg == "-e" || arg == "--regexp":
				if i+1 < len(args) {
					value = args[i+1]
				}
			case arg == "-f" || arg == "--file" || strings.HasPrefix(arg, "--file=") || strings.HasPrefix(arg, "-f"):
			case strings.HasPrefix(arg, "--regexp="):
				value = arg[9:]
			case strings.HasPrefix(arg, "-e"):
				value = arg[2:]
			default:
				pattern = false
			}
			if pattern && !patterned {
				query, patterned = value, true
			}
			if rgValueFlag.MatchString(arg) || grepValueFlag.MatchString(arg) {
				i++
			}
			continue
		}
		if !patterned {
			query, patterned = arg, true
			continue
		}
		if arg != "-" {
			paths = append(paths, arg)
		}
	}
	return query, paths, patterned
}

// operationTarget is the single path, and for a search its pattern, named by a
// read, listing or search command. Anything less certain stays empty.
func operationTarget(words []string, kind string) (path, query string) {
	if kind != "read" && kind != "list" && kind != "search" {
		return "", ""
	}
	one := func(paths []string) string {
		if len(paths) == 1 {
			return paths[0]
		}
		return ""
	}
	command, args := commandArgs(words)
	switch {
	case command == "sed":
		files, _ := sedPrintFiles(args)
		return one(files), ""
	case command == "find":
		var paths []string
		for i, arg := range args {
			if strings.HasPrefix(arg, "-") || arg == "!" {
				for j := i; j+1 < len(args) && kind == "search"; j++ {
					if findNameFlag.MatchString(args[j]) {
						query = args[j+1]
						break
					}
				}
				break
			}
			paths = append(paths, arg)
		}
		return one(paths), query
	case searchCommand(command) && kind == "search":
		query, paths, _ := searchOperands(args)
		return one(paths), query
	}
	var paths []string
	options := true
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if options && arg == "--" {
			options = false
			continue
		}
		if options && strings.HasPrefix(arg, "-") && arg != "-" {
			switch {
			case searchCommand(command):
				if rgValueFlag.MatchString(arg) || grepValueFlag.MatchString(arg) {
					i++
				}
			case (command == "head" || command == "tail") && headValueFlag.MatchString(arg):
				i++
			case command == "cat" || command == "head" || command == "tail" || command == "nl":
			case command == "ls" && lsPlainFlag.MatchString(arg):
			default:
				return "", ""
			}
			continue
		}
		if arg != "-" {
			paths = append(paths, arg)
		}
	}
	return one(paths), ""
}

// pipeFilter reports a pipeline stage that only narrows or formats the output
// of the stage before it, and so does not change what the pipeline did.
func pipeFilter(words []string) bool {
	command, args := commandArgs(words)
	operands := func(valueFlag *regexp.Regexp) int {
		count := 0
		for i := 0; i < len(args); i++ {
			if strings.HasPrefix(args[i], "-") && args[i] != "-" {
				if valueFlag != nil && valueFlag.MatchString(args[i]) {
					i++
				}
				continue
			}
			count++
		}
		return count
	}
	switch command {
	case "wc", "tr", "cut", "column":
		return true
	case "sort":
		for _, arg := range args {
			if strings.HasPrefix(arg, "-o") || strings.HasPrefix(arg, "--output") {
				return false
			}
		}
		return true
	case "uniq":
		return operands(nil) == 0
	case "head", "tail":
		return operands(headValueFlag) == 0
	case "rg", "ripgrep", "grep", "egrep", "fgrep":
		for _, arg := range args {
			if arg == "--files" {
				return false
			}
		}
		_, paths, patterned := searchOperands(args)
		return patterned && len(paths) == 0
	case "sed":
		files, ok := sedPrintFiles(args)
		return ok && len(files) == 0
	}
	return false
}

var sedPrint = regexp.MustCompile(`^\s*(?:(?:[0-9]+|\$)\s*(?:,\s*(?:[0-9]+|\$)\s*)?)?p\s*$`)
var sedPrintFlag = regexp.MustCompile(`^-[nEr]+$`)

// Print-only scripts read a file. sed also supports commands that write files
// or execute programs without -i, so other scripts remain generic.
func sedPrintFiles(args []string) ([]string, bool) {
	var scripts, files []string
	options := true
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if options && arg == "--" {
			options = false
			continue
		}
		if options && strings.HasPrefix(arg, "-") {
			switch {
			case sedPrintFlag.MatchString(arg), arg == "--quiet", arg == "--silent", arg == "--regexp-extended":
			case arg == "-e", arg == "--expression":
				i++
				if i == len(args) {
					return nil, false
				}
				scripts = append(scripts, args[i])
			case strings.HasPrefix(arg, "--expression="):
				scripts = append(scripts, arg[13:])
			case strings.HasPrefix(arg, "-e") && len(arg) > 2:
				scripts = append(scripts, arg[2:])
			default:
				return nil, false
			}
		} else if len(scripts) == 0 {
			scripts = append(scripts, arg)
		} else {
			files = append(files, arg)
		}
	}
	if len(scripts) == 0 {
		return nil, false
	}
	for _, script := range scripts {
		count := 0
		for _, item := range strings.FieldsFunc(script, func(r rune) bool { return r == ';' || r == '\n' }) {
			if strings.TrimSpace(item) == "" {
				continue
			}
			if !sedPrint.MatchString(item) {
				return nil, false
			}
			count++
		}
		if count == 0 {
			return nil, false
		}
	}
	return files, true
}

// shellTarget is the path and pattern of a command that does one thing.
func shellTarget(source string, depth int) (path, query string) {
	if depth > 4 || len(source) > 65536 {
		return "", ""
	}
	commands, piped, ok := shellParse(source)
	if !ok || len(commands) == 0 {
		return "", ""
	}
	if len(commands) == 1 {
		if script, ok := shellScript(commands[0]); ok {
			return shellTarget(script, depth+1)
		}
	}
	for i, words := range commands[1:] {
		if !piped[i+1] || !pipeFilter(words) {
			return "", ""
		}
	}
	return operationTarget(commands[0], commandKind(commands[0], depth))
}

func shellCommandKind(source string, depth int) string {
	if depth > 4 || len(source) > 65536 {
		return "shell"
	}
	commands, piped, ok := shellParse(source)
	if !ok || len(commands) == 0 || len(commands) > 64 {
		return "shell"
	}
	kind := commandKind(commands[0], depth)
	for i, words := range commands[1:] {
		if piped[i+1] && pipeFilter(words) {
			continue
		}
		if commandKind(words, depth) != kind {
			return "shell"
		}
	}
	return kind
}

var shellFlag = regexp.MustCompile(`^-[lc]+$`)
var rgValueFlag = regexp.MustCompile(`^(?:-[efgtTmjErABC]|--(?:regexp|file|glob|iglob|type|type-not|ignore-file|pre|pre-glob|encoding|max-count|max-depth|threads|replace|sort|sortr|color|colors|context|after-context|before-context|path-separator|hostname-bin|hyperlink-format))$`)

func commandKind(words []string, depth int) string {
	command, args := commandArgs(words)
	switch command {
	case "sh", "bash", "zsh", "dash", "ksh", "fish":
		for i, arg := range args {
			if !shellFlag.MatchString(arg) {
				break
			}
			if strings.ContainsRune(arg, 'c') {
				if len(args) == i+2 {
					return shellCommandKind(args[i+1], depth+1)
				}
				break
			}
		}
	case "rg", "ripgrep":
		for i := 0; i < len(args); i++ {
			arg := args[i]
			if arg == "--" {
				break
			}
			if arg == "--files" {
				return "list"
			}
			if rgValueFlag.MatchString(arg) {
				i++
			}
		}
		return "search"
	case "grep", "egrep", "fgrep":
		return "search"
	case "ls", "tree":
		return "list"
	case "find":
		for _, arg := range args {
			switch arg {
			case "-delete", "-exec", "-execdir", "-ok", "-okdir", "-fprint", "-fprint0", "-fprintf", "-fls":
				return "shell"
			}
		}
		for _, arg := range args {
			if findNameFlag.MatchString(arg) {
				return "search"
			}
		}
		return "list"
	case "cat", "head", "tail", "less", "more", "nl":
		return "read"
	case "sed":
		if _, ok := sedPrintFiles(args); ok {
			return "read"
		}
	}
	return "shell"
}
