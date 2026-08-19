package cursor

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"

	agentpb "github.com/DouDOU-start/airgate-core/internal/relay/cursor/proto/agentpb"
)

var grepContentLinePattern = regexp.MustCompile(`^(.+?):(\d+):(.*)$`)
var grepContextLinePattern = regexp.MustCompile(`^(.+?)-(\d+)-(.*)$`)
var claudeReadLinePattern = regexp.MustCompile(`^\s*\d+→(.*)$`)

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

// Claude Code 的 Read 工具会把正文渲染成“行号→内容”。Cursor ReadResult
// 需要原始正文，因此仅在输出从首个非空行开始连续符合该格式时去掉前缀。
func normalizeClaudeReadOutput(text string) string {
	lines := strings.Split(text, "\n")
	content := make([]string, 0, len(lines))
	started := false
	for _, line := range lines {
		match := claudeReadLinePattern.FindStringSubmatch(line)
		if match != nil {
			started = true
			content = append(content, match[1])
			continue
		}
		if !started && strings.TrimSpace(line) == "" {
			continue
		}
		if !started {
			return text
		}
		// 行号正文后的系统提示不是文件内容，不继续透传。
		break
	}
	if !started {
		return text
	}
	return strings.Join(content, "\n")
}

func setToolArg(target map[string]any, definition *agentpb.McpToolDefinition, value any, candidates ...string) bool {
	property, ok := toolSchemaProperty(definition, candidates...)
	if !ok {
		return false
	}
	target[property] = value
	return true
}

func (s *session) bashToolCall(
	ex *agentpb.ExecServerMessage,
	call pendingExecCall,
	command string,
	description string,
	timeout any,
	toolCallID string,
) bool {
	name, definition := s.downstreamTool("Bash", "Shell")
	if name == "" {
		return false
	}
	call.name = name
	call.command = command
	toolArgs := map[string]any{requiredToolProperty(definition, "command"): command}
	if description != "" {
		setToolArg(toolArgs, definition, description, "description")
	}
	if timeout != nil {
		setToolArg(toolArgs, definition, timeout, "timeout")
	}
	s.queueExecTool(ex, call, toolArgs, toolCallID)
	return true
}

func (s *session) onLsExec(ex *agentpb.ExecServerMessage, args *agentpb.LsArgs) bool {
	if args == nil {
		return false
	}
	path := args.GetPath()
	if path == "" {
		path = "."
	}
	command := "ls -1Ap"
	for _, ignore := range args.GetIgnore() {
		command += " --ignore=" + shellQuote(ignore)
	}
	command += " -- " + shellQuote(path)
	var timeout any
	if args.TimeoutMs != nil {
		timeout = args.GetTimeoutMs()
	}
	return s.bashToolCall(ex, pendingExecCall{kind: pendingExecLs, path: path}, command, "列出目录内容", timeout, args.GetToolCallId())
}

func (s *session) onDeleteExec(ex *agentpb.ExecServerMessage, args *agentpb.DeleteArgs) bool {
	if args == nil {
		return false
	}
	command := "rm -f -- " + shellQuote(args.GetPath())
	return s.bashToolCall(ex, pendingExecCall{kind: pendingExecDelete, path: args.GetPath()}, command, "删除文件", nil, args.GetToolCallId())
}

func (s *session) onGrepExec(ex *agentpb.ExecServerMessage, args *agentpb.GrepArgs) bool {
	if args == nil || strings.TrimSpace(args.GetPattern()) == "" {
		return false
	}
	call := pendingExecCall{
		kind: pendingExecGrep, path: args.GetPath(), pattern: args.GetPattern(),
		outputMode: args.GetOutputMode(), grepOffset: cloneInt32(args.Offset),
	}
	if call.outputMode == "" {
		call.outputMode = "content"
	}
	if name, definition := s.downstreamTool("Grep"); name != "" {
		toolArgs := map[string]any{requiredToolProperty(definition, "pattern"): args.GetPattern()}
		if args.Path != nil {
			setToolArg(toolArgs, definition, args.GetPath(), "path")
		}
		if args.Glob != nil {
			setToolArg(toolArgs, definition, args.GetGlob(), "glob")
		}
		if args.OutputMode != nil {
			setToolArg(toolArgs, definition, args.GetOutputMode(), "output_mode")
		}
		if args.ContextBefore != nil {
			setToolArg(toolArgs, definition, args.GetContextBefore(), "-B", "context_before")
		}
		if args.ContextAfter != nil {
			setToolArg(toolArgs, definition, args.GetContextAfter(), "-A", "context_after")
		}
		if args.Context != nil {
			setToolArg(toolArgs, definition, args.GetContext(), "-C", "context")
		}
		if args.CaseInsensitive != nil {
			setToolArg(toolArgs, definition, args.GetCaseInsensitive(), "-i", "case_insensitive")
		}
		if args.Type != nil {
			setToolArg(toolArgs, definition, args.GetType(), "type")
		}
		if args.HeadLimit != nil {
			setToolArg(toolArgs, definition, args.GetHeadLimit(), "head_limit")
		}
		if args.Multiline != nil {
			setToolArg(toolArgs, definition, args.GetMultiline(), "multiline")
		}
		if args.Offset != nil {
			setToolArg(toolArgs, definition, args.GetOffset(), "offset")
		}
		call.name = name
		s.queueExecTool(ex, call, toolArgs, args.GetToolCallId())
		return true
	}
	return s.bashToolCall(ex, call, buildRipgrepCommand(args), "搜索文件内容", nil, args.GetToolCallId())
}

func buildRipgrepCommand(args *agentpb.GrepArgs) string {
	parts := []string{"rg", "--color=never"}
	switch args.GetOutputMode() {
	case "files_with_matches":
		parts = append(parts, "--files-with-matches")
	case "count":
		parts = append(parts, "--count")
	default:
		parts = append(parts, "--line-number")
	}
	if args.ContextBefore != nil {
		parts = append(parts, "-B", strconv.Itoa(int(args.GetContextBefore())))
	}
	if args.ContextAfter != nil {
		parts = append(parts, "-A", strconv.Itoa(int(args.GetContextAfter())))
	}
	if args.Context != nil {
		parts = append(parts, "-C", strconv.Itoa(int(args.GetContext())))
	}
	if args.GetCaseInsensitive() {
		parts = append(parts, "-i")
	}
	if args.Type != nil && args.GetType() != "" {
		parts = append(parts, "--type", shellQuote(args.GetType()))
	}
	if args.Glob != nil && args.GetGlob() != "" {
		parts = append(parts, "--glob", shellQuote(args.GetGlob()))
	}
	if args.GetMultiline() {
		parts = append(parts, "-U", "--multiline-dotall")
	}
	if args.Sort != nil && args.GetSort() != "" && args.GetSort() != "none" {
		flag := "--sort"
		if args.SortAscending != nil && !args.GetSortAscending() {
			flag = "--sortr"
		}
		parts = append(parts, flag, shellQuote(args.GetSort()))
	}
	path := args.GetPath()
	if path == "" {
		path = "."
	}
	parts = append(parts, "--", shellQuote(args.GetPattern()), shellQuote(path))
	command := strings.Join(parts, " ") + "; status=$?; [ \"$status\" -le 1 ] || exit \"$status\""
	if args.Offset != nil || args.HeadLimit != nil {
		start := int(args.GetOffset()) + 1
		if start < 1 {
			start = 1
		}
		end := "$"
		if args.HeadLimit != nil && args.GetHeadLimit() > 0 {
			end = strconv.Itoa(start + int(args.GetHeadLimit()) - 1)
		}
		command = "(" + command + ") | sed -n " + shellQuote(fmt.Sprintf("%d,%sp", start, end))
	}
	return command
}

func (s *session) onFetchExec(ex *agentpb.ExecServerMessage, args *agentpb.FetchArgs) bool {
	if args == nil {
		return false
	}
	call := pendingExecCall{kind: pendingExecFetch, url: args.GetUrl()}
	if name, definition := s.downstreamTool("WebFetch"); name != "" {
		toolArgs := map[string]any{requiredToolProperty(definition, "url"): args.GetUrl()}
		setToolArg(toolArgs, definition, "获取该 URL 的完整正文内容。", "prompt")
		call.name = name
		s.queueExecTool(ex, call, toolArgs, args.GetToolCallId())
		return true
	}
	command := "curl -L --fail --silent --show-error --max-time 30 -- " + shellQuote(args.GetUrl())
	return s.bashToolCall(ex, call, command, "获取网页内容", 30000, args.GetToolCallId())
}

func (s *session) onSubagentExec(ex *agentpb.ExecServerMessage, args *agentpb.SubagentArgs) bool {
	if args == nil {
		return false
	}
	name, definition := s.downstreamTool("Task", "Agent")
	if name == "" {
		return false
	}
	toolArgs := map[string]any{requiredToolProperty(definition, "prompt"): args.GetPrompt()}
	setToolArg(toolArgs, definition, args.GetSubagentType(), "subagent_type", "agent_type")
	setToolArg(toolArgs, definition, "执行 Cursor 请求的子代理任务", "description")
	setToolArg(toolArgs, definition, false, "run_in_background")
	s.queueExecTool(ex, pendingExecCall{kind: pendingExecSubagent, name: name}, toolArgs, args.GetToolCallId())
	return true
}

func (s *session) onDiagnosticsExec(ex *agentpb.ExecServerMessage, args *agentpb.DiagnosticsArgs) bool {
	if args == nil {
		return false
	}
	name, definition := s.downstreamTool("LSP", "Diagnostics", "mcp__ide__getDiagnostics")
	if name == "" {
		return false
	}
	toolArgs := make(map[string]any)
	setToolArg(toolArgs, definition, "diagnostics", "action")
	if !setToolArg(toolArgs, definition, args.GetPath(), "file", "file_path", "path", "uri") {
		toolArgs[requiredToolProperty(definition, "file", "file_path", "path", "uri")] = args.GetPath()
	}
	s.queueExecTool(ex, pendingExecCall{
		kind: pendingExecDiagnostics, name: name, path: args.GetPath(),
	}, toolArgs, args.GetToolCallId())
	return true
}

func (s *session) onPiReadExec(ex *agentpb.ExecServerMessage, args *agentpb.PiReadExecArgs) bool {
	name, definition := s.downstreamTool("Read")
	if name == "" || args == nil {
		return false
	}
	toolArgs := map[string]any{requiredToolProperty(definition, "file_path", "path"): args.GetPath()}
	if args.Offset != nil {
		setToolArg(toolArgs, definition, args.GetOffset(), "offset")
	}
	if args.Limit != nil {
		setToolArg(toolArgs, definition, args.GetLimit(), "limit")
	}
	s.queueExecTool(ex, pendingExecCall{kind: pendingExecPiRead, name: name, path: args.GetPath()}, toolArgs, "")
	return true
}

func (s *session) onPiBashExec(ex *agentpb.ExecServerMessage, args *agentpb.PiBashExecArgs) bool {
	if args == nil {
		return false
	}
	var timeout any
	if args.Timeout != nil && args.GetTimeout() > 0 {
		timeout = args.GetTimeout()
	}
	return s.bashToolCall(ex, pendingExecCall{kind: pendingExecPiBash}, args.GetCommand(), "执行命令", timeout, "")
}

func (s *session) onPiWriteExec(ex *agentpb.ExecServerMessage, args *agentpb.PiWriteExecArgs) bool {
	name, definition := s.downstreamTool("Write")
	if name == "" || args == nil {
		return false
	}
	toolArgs := map[string]any{
		requiredToolProperty(definition, "file_path", "path"): args.GetPath(),
		requiredToolProperty(definition, "content"):           args.GetContent(),
	}
	s.queueExecTool(ex, pendingExecCall{kind: pendingExecPiWrite, name: name, path: args.GetPath()}, toolArgs, "")
	return true
}

func (s *session) onPiEditExec(ex *agentpb.ExecServerMessage, args *agentpb.PiEditExecArgs) bool {
	if args == nil || len(args.GetEdits()) == 0 {
		return false
	}
	if len(args.GetEdits()) > 1 {
		name, definition := s.downstreamTool("MultiEdit")
		if name != "" {
			edits := make([]map[string]any, 0, len(args.GetEdits()))
			for _, edit := range args.GetEdits() {
				edits = append(edits, map[string]any{"old_string": edit.GetOldText(), "new_string": edit.GetNewText()})
			}
			toolArgs := map[string]any{
				requiredToolProperty(definition, "file_path", "path"): args.GetPath(),
				requiredToolProperty(definition, "edits"):             edits,
			}
			s.queueExecTool(ex, pendingExecCall{kind: pendingExecPiEdit, name: name, path: args.GetPath()}, toolArgs, "")
			return true
		}
		// Claude Code 新版通常不再暴露 MultiEdit。通过其 Bash 工具在本地一次性
		// 应用全部替换，保持一个 Cursor exec 对应一个下游工具结果。
		payload := struct {
			Path  string              `json:"path"`
			Edits []map[string]string `json:"edits"`
		}{Path: args.GetPath(), Edits: make([]map[string]string, 0, len(args.GetEdits()))}
		for _, edit := range args.GetEdits() {
			payload.Edits = append(payload.Edits, map[string]string{
				"old": edit.GetOldText(), "new": edit.GetNewText(),
			})
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return false
		}
		script := `const fs=require("fs");const p=JSON.parse(Buffer.from(process.argv[1],"base64").toString("utf8"));let s=fs.readFileSync(p.path,"utf8");for(const e of p.edits){const i=s.indexOf(e.old);if(i<0)throw new Error("找不到待替换文本");s=s.slice(0,i)+e.new+s.slice(i+e.old.length)}fs.writeFileSync(p.path,s)`
		command := "node -e " + shellQuote(script) + " " + shellQuote(base64.StdEncoding.EncodeToString(encoded))
		return s.bashToolCall(ex, pendingExecCall{
			kind: pendingExecPiEdit, path: args.GetPath(),
		}, command, "批量编辑文件", nil, "")
	}
	name, definition := s.downstreamTool("Edit")
	if name == "" {
		return false
	}
	edit := args.GetEdits()[0]
	toolArgs := map[string]any{
		requiredToolProperty(definition, "file_path", "path"):      args.GetPath(),
		requiredToolProperty(definition, "old_string", "old_text"): edit.GetOldText(),
		requiredToolProperty(definition, "new_string", "new_text"): edit.GetNewText(),
	}
	s.queueExecTool(ex, pendingExecCall{kind: pendingExecPiEdit, name: name, path: args.GetPath()}, toolArgs, "")
	return true
}

func (s *session) onPiGrepExec(ex *agentpb.ExecServerMessage, args *agentpb.PiGrepExecArgs) bool {
	if args == nil || strings.TrimSpace(args.GetPattern()) == "" {
		return false
	}
	pattern := args.GetPattern()
	if args.GetLiteral() {
		pattern = regexp.QuoteMeta(pattern)
	}
	call := pendingExecCall{kind: pendingExecPiGrep, pattern: pattern, path: args.GetPath()}
	if name, definition := s.downstreamTool("Grep"); name != "" {
		toolArgs := map[string]any{requiredToolProperty(definition, "pattern"): pattern}
		if args.Path != nil {
			setToolArg(toolArgs, definition, args.GetPath(), "path")
		}
		if args.Glob != nil {
			setToolArg(toolArgs, definition, args.GetGlob(), "glob")
		}
		if args.IgnoreCase != nil {
			setToolArg(toolArgs, definition, args.GetIgnoreCase(), "-i", "case_insensitive")
		}
		if args.Context != nil {
			setToolArg(toolArgs, definition, args.GetContext(), "-C", "context")
		}
		if args.Limit != nil {
			setToolArg(toolArgs, definition, args.GetLimit(), "head_limit", "limit")
		}
		call.name = name
		s.queueExecTool(ex, call, toolArgs, "")
		return true
	}
	path := args.GetPath()
	if path == "" {
		path = "."
	}
	parts := []string{"rg", "--color=never", "--line-number"}
	if args.GetIgnoreCase() {
		parts = append(parts, "-i")
	}
	if args.Context != nil {
		parts = append(parts, "-C", strconv.Itoa(int(args.GetContext())))
	}
	if args.Glob != nil && args.GetGlob() != "" {
		parts = append(parts, "--glob", shellQuote(args.GetGlob()))
	}
	parts = append(parts, "--", shellQuote(pattern), shellQuote(path))
	command := strings.Join(parts, " ") + "; status=$?; [ \"$status\" -le 1 ] || exit \"$status\""
	if args.Limit != nil && args.GetLimit() > 0 {
		command = "(" + command + ") | head -n " + strconv.Itoa(int(args.GetLimit()))
	}
	return s.bashToolCall(ex, call, command, "搜索文件内容", nil, "")
}

func (s *session) onPiFindExec(ex *agentpb.ExecServerMessage, args *agentpb.PiFindExecArgs) bool {
	if args == nil {
		return false
	}
	call := pendingExecCall{kind: pendingExecPiFind, pattern: args.GetPattern(), path: args.GetPath()}
	if name, definition := s.downstreamTool("Glob"); name != "" {
		toolArgs := map[string]any{requiredToolProperty(definition, "pattern"): args.GetPattern()}
		if args.Path != nil {
			setToolArg(toolArgs, definition, args.GetPath(), "path")
		}
		call.name = name
		s.queueExecTool(ex, call, toolArgs, "")
		return true
	}
	path := args.GetPath()
	if path == "" {
		path = "."
	}
	command := "rg --files --glob " + shellQuote(args.GetPattern()) + " -- " + shellQuote(path)
	if args.Limit != nil && args.GetLimit() > 0 {
		command += " | head -n " + strconv.Itoa(int(args.GetLimit()))
	}
	return s.bashToolCall(ex, call, command, "查找文件", nil, "")
}

func (s *session) onPiLsExec(ex *agentpb.ExecServerMessage, args *agentpb.PiLsExecArgs) bool {
	if args == nil {
		return false
	}
	path := args.GetPath()
	if path == "" {
		path = "."
	}
	command := "ls -1Ap -- " + shellQuote(path)
	if args.Limit != nil && args.GetLimit() > 0 {
		command += " | head -n " + strconv.Itoa(int(args.GetLimit()))
	}
	return s.bashToolCall(ex, pendingExecCall{kind: pendingExecPiLs, path: path}, command, "列出目录内容", nil, "")
}

func (s *session) onMiniSweBashExec(ex *agentpb.ExecServerMessage, args *agentpb.ShellArgs) bool {
	if args == nil {
		return false
	}
	var timeout any
	if args.GetTimeout() > 0 {
		timeout = args.GetTimeout()
	}
	return s.bashToolCall(ex, pendingExecCall{
		kind: pendingExecMiniSweBash, workingDirectory: args.GetWorkingDirectory(),
	}, args.GetCommand(), args.GetDescription(), timeout, args.GetToolCallId())
}

func cloneInt32(value *int32) *int32 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func buildDeleteResumeResult(call pendingExecCall, result NMessage) *agentpb.DeleteResult {
	if result.IsError {
		return &agentpb.DeleteResult{Result: &agentpb.DeleteResult_Error{Error: &agentpb.DeleteError{
			Path: call.path, Error: errorResultText(result, "删除文件失败"),
		}}}
	}
	return &agentpb.DeleteResult{Result: &agentpb.DeleteResult_Success{Success: &agentpb.DeleteSuccess{
		Path: call.path, DeletedFile: call.path,
	}}}
}

func buildLsResumeResult(call pendingExecCall, result NMessage) *agentpb.LsResult {
	if result.IsError {
		return &agentpb.LsResult{Result: &agentpb.LsResult_Error{Error: &agentpb.LsError{
			Path: call.path, Error: errorResultText(result, "列出目录失败"),
		}}}
	}
	rootPath := call.path
	if rootPath == "" {
		rootPath = "."
	}
	root := &agentpb.LsDirectoryTreeNode{
		AbsPath: rootPath, ChildrenWereProcessed: true,
		FullSubtreeExtensionCounts: make(map[string]int32),
	}
	for _, entry := range outputLines(partsText(result.Content)) {
		if strings.HasSuffix(entry, "/") {
			name := strings.TrimSuffix(entry, "/")
			root.ChildrenDirs = append(root.ChildrenDirs, &agentpb.LsDirectoryTreeNode{
				AbsPath:                    joinDisplayPath(rootPath, name),
				FullSubtreeExtensionCounts: make(map[string]int32),
			})
			continue
		}
		root.ChildrenFiles = append(root.ChildrenFiles, &agentpb.LsDirectoryTreeNode_File{Name: entry})
	}
	root.NumFiles = int32(len(root.ChildrenFiles))
	return &agentpb.LsResult{Result: &agentpb.LsResult_Success{Success: &agentpb.LsSuccess{DirectoryTreeRoot: root}}}
}

func joinDisplayPath(root, name string) string {
	if root == "/" {
		return "/" + name
	}
	return strings.TrimSuffix(root, "/") + "/" + name
}

func outputLines(text string) []string {
	lines := make([]string, 0)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "[") || strings.HasPrefix(strings.ToLower(line), "no matches") {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

func buildGrepResumeResult(call pendingExecCall, result NMessage) *agentpb.GrepResult {
	if result.IsError {
		return &agentpb.GrepResult{Result: &agentpb.GrepResult_Error{Error: &agentpb.GrepError{
			Error: errorResultText(result, "搜索文件失败"),
		}}}
	}
	workspaceKey := call.path
	if workspaceKey == "" {
		workspaceKey = "."
	}
	union := buildGrepUnion(call, outputLines(partsText(result.Content)))
	return &agentpb.GrepResult{Result: &agentpb.GrepResult_Success{Success: &agentpb.GrepSuccess{
		Pattern: call.pattern, Path: call.path, OutputMode: call.outputMode,
		WorkspaceResults: map[string]*agentpb.GrepUnionResult{workspaceKey: union},
	}}}
}

func buildGrepUnion(call pendingExecCall, lines []string) *agentpb.GrepUnionResult {
	switch call.outputMode {
	case "files_with_matches":
		files := &agentpb.GrepFilesResult{Files: lines, TotalFiles: int32(len(lines)), OffsetApplied: call.grepOffset}
		return &agentpb.GrepUnionResult{Result: &agentpb.GrepUnionResult_Files{Files: files}}
	case "count":
		counts := make([]*agentpb.GrepFileCount, 0, len(lines))
		var total int32
		for _, line := range lines {
			separator := strings.LastIndex(line, ":")
			if separator <= 0 {
				continue
			}
			count, err := strconv.ParseInt(strings.TrimSpace(line[separator+1:]), 10, 32)
			if err != nil {
				continue
			}
			counts = append(counts, &agentpb.GrepFileCount{File: line[:separator], Count: int32(count)})
			total += int32(count)
		}
		value := &agentpb.GrepCountResult{
			Counts: counts, TotalFiles: int32(len(counts)), TotalMatches: total, OffsetApplied: call.grepOffset,
		}
		return &agentpb.GrepUnionResult{Result: &agentpb.GrepUnionResult_Count{Count: value}}
	default:
		return buildGrepContentUnion(lines, call.grepOffset)
	}
}

func buildGrepContentUnion(lines []string, offset *int32) *agentpb.GrepUnionResult {
	byFile := make(map[string][]*agentpb.GrepContentMatch)
	order := make([]string, 0)
	var matched int32
	for _, line := range lines {
		parts := grepContentLinePattern.FindStringSubmatch(line)
		contextLine := false
		if parts == nil {
			parts = grepContextLinePattern.FindStringSubmatch(line)
			contextLine = parts != nil
		}
		if parts == nil {
			continue
		}
		lineNumber, err := strconv.ParseInt(parts[2], 10, 32)
		if err != nil {
			continue
		}
		if _, exists := byFile[parts[1]]; !exists {
			order = append(order, parts[1])
		}
		byFile[parts[1]] = append(byFile[parts[1]], &agentpb.GrepContentMatch{
			LineNumber: int32(lineNumber), Content: strings.TrimPrefix(parts[3], " "), IsContextLine: contextLine,
		})
		if !contextLine {
			matched++
		}
	}
	matches := make([]*agentpb.GrepFileMatch, 0, len(order))
	var total int32
	for _, file := range order {
		fileMatches := byFile[file]
		total += int32(len(fileMatches))
		matches = append(matches, &agentpb.GrepFileMatch{File: file, Matches: fileMatches})
	}
	content := &agentpb.GrepContentResult{
		Matches: matches, TotalLines: total, TotalMatchedLines: matched, OffsetApplied: offset,
	}
	return &agentpb.GrepUnionResult{Result: &agentpb.GrepUnionResult_Content{Content: content}}
}

func buildFetchResumeResult(call pendingExecCall, result NMessage) *agentpb.FetchResult {
	if result.IsError {
		return &agentpb.FetchResult{Result: &agentpb.FetchResult_Error{Error: &agentpb.FetchError{
			Url: call.url, Error: errorResultText(result, "获取网页失败"),
		}}}
	}
	return &agentpb.FetchResult{Result: &agentpb.FetchResult_Success{Success: &agentpb.FetchSuccess{
		Url: call.url, Content: partsText(result.Content), StatusCode: 200, ContentType: "text/plain",
	}}}
}

func buildSubagentResumeResult(result NMessage) *agentpb.SubagentResult {
	text := partsText(result.Content)
	if result.IsError {
		return &agentpb.SubagentResult{Result: &agentpb.SubagentResult_Error{Error: &agentpb.SubagentError{
			Error: errorResultText(result, "子代理执行失败"),
		}}}
	}
	return &agentpb.SubagentResult{Result: &agentpb.SubagentResult_Success{Success: &agentpb.SubagentSuccess{
		AgentId: uuid.NewString(), FinalMessage: &text,
	}}}
}

func buildDiagnosticsResumeResult(call pendingExecCall, result NMessage) *agentpb.DiagnosticsResult {
	if result.IsError {
		return &agentpb.DiagnosticsResult{Result: &agentpb.DiagnosticsResult_Error{Error: &agentpb.DiagnosticsError{
			Error: errorResultText(result, "获取诊断信息失败"),
		}}}
	}
	return &agentpb.DiagnosticsResult{Result: &agentpb.DiagnosticsResult_Success{Success: &agentpb.DiagnosticsSuccess{
		Path: call.path, Diagnostics: []*agentpb.Diagnostic{}, TotalDiagnostics: 0,
	}}}
}

func buildMcpStateResult(
	tools []*agentpb.McpToolDefinition,
	serverIdentifiers []string,
) *agentpb.McpStateExecResult {
	wanted := make(map[string]struct{}, len(serverIdentifiers))
	for _, identifier := range serverIdentifiers {
		wanted[identifier] = struct{}{}
	}
	byProvider := make(map[string][]*agentpb.McpToolDefinition)
	order := make([]string, 0)
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		identifier := tool.GetProviderIdentifier()
		if len(wanted) > 0 {
			if _, ok := wanted[identifier]; !ok {
				continue
			}
		}
		if _, exists := byProvider[identifier]; !exists {
			order = append(order, identifier)
		}
		byProvider[identifier] = append(byProvider[identifier], tool)
	}
	servers := make([]*agentpb.McpStateServer, 0, len(order))
	for _, identifier := range order {
		status := "connected"
		servers = append(servers, &agentpb.McpStateServer{
			ServerName: identifier, ServerIdentifier: identifier,
			Tools: byProvider[identifier], Status: &status,
		})
	}
	return &agentpb.McpStateExecResult{Result: &agentpb.McpStateExecResult_Success{
		Success: &agentpb.McpStateSuccess{Servers: servers},
	}}
}

// 网关不执行 Cursor 客户端 Hook，但必须返回与请求 oneof 对应的空响应。
// 这表示“没有 Hook 附加行为”，同时避免服务端等待错误类型的结果。
func buildNeutralHookResult(request *agentpb.ExecuteHookRequest) *agentpb.ExecuteHookResult {
	if request == nil {
		return nil
	}
	response := &agentpb.ExecuteHookResponse{}
	switch request.GetRequest().(type) {
	case *agentpb.ExecuteHookRequest_PreCompact:
		response.Response = &agentpb.ExecuteHookResponse_PreCompact{PreCompact: &agentpb.PreCompactRequestResponse{}}
	case *agentpb.ExecuteHookRequest_SubagentStart:
		response.Response = &agentpb.ExecuteHookResponse_SubagentStart{SubagentStart: &agentpb.SubagentStartRequestResponse{}}
	case *agentpb.ExecuteHookRequest_SubagentStop:
		response.Response = &agentpb.ExecuteHookResponse_SubagentStop{SubagentStop: &agentpb.SubagentStopRequestResponse{}}
	case *agentpb.ExecuteHookRequest_PreToolUse:
		response.Response = &agentpb.ExecuteHookResponse_PreToolUse{PreToolUse: &agentpb.PreToolUseRequestResponse{}}
	case *agentpb.ExecuteHookRequest_PostToolUse:
		response.Response = &agentpb.ExecuteHookResponse_PostToolUse{PostToolUse: &agentpb.PostToolUseRequestResponse{}}
	case *agentpb.ExecuteHookRequest_PostToolUseFailure:
		response.Response = &agentpb.ExecuteHookResponse_PostToolUseFailure{PostToolUseFailure: &agentpb.PostToolUseFailureRequestResponse{}}
	case *agentpb.ExecuteHookRequest_BeforeSubmitPrompt:
		response.Response = &agentpb.ExecuteHookResponse_BeforeSubmitPrompt{BeforeSubmitPrompt: &agentpb.BeforeSubmitPromptRequestResponse{}}
	case *agentpb.ExecuteHookRequest_AfterAgentResponse:
		response.Response = &agentpb.ExecuteHookResponse_AfterAgentResponse{AfterAgentResponse: &agentpb.AfterAgentResponseRequestResponse{}}
	case *agentpb.ExecuteHookRequest_AfterAgentThought:
		response.Response = &agentpb.ExecuteHookResponse_AfterAgentThought{AfterAgentThought: &agentpb.AfterAgentThoughtRequestResponse{}}
	case *agentpb.ExecuteHookRequest_Stop:
		response.Response = &agentpb.ExecuteHookResponse_Stop{Stop: &agentpb.StopRequestResponse{}}
	default:
		return nil
	}
	return &agentpb.ExecuteHookResult{Response: response}
}

func buildPiReadResumeResult(result NMessage) *agentpb.PiReadExecResult {
	if result.IsError {
		return &agentpb.PiReadExecResult{Result: &agentpb.PiReadExecResult_Error{Error: &agentpb.PiReadExecError{Error: errorResultText(result, "读取文件失败")}}}
	}
	return &agentpb.PiReadExecResult{Result: &agentpb.PiReadExecResult_Success{Success: &agentpb.PiReadExecSuccess{Output: partsText(result.Content)}}}
}

func buildPiBashResumeResult(result NMessage) *agentpb.PiBashExecResult {
	if result.IsError {
		return &agentpb.PiBashExecResult{Result: &agentpb.PiBashExecResult_Error{Error: &agentpb.PiBashExecError{Error: errorResultText(result, "命令执行失败")}}}
	}
	return &agentpb.PiBashExecResult{Result: &agentpb.PiBashExecResult_Success{Success: &agentpb.PiBashExecSuccess{Output: partsText(result.Content)}}}
}

func buildPiEditResumeResult(result NMessage) *agentpb.PiEditExecResult {
	if result.IsError {
		return &agentpb.PiEditExecResult{Result: &agentpb.PiEditExecResult_Error{Error: &agentpb.PiEditExecError{Error: errorResultText(result, "编辑文件失败")}}}
	}
	return &agentpb.PiEditExecResult{Result: &agentpb.PiEditExecResult_Success{Success: &agentpb.PiEditExecSuccess{Output: partsText(result.Content)}}}
}

func buildPiWriteResumeResult(result NMessage) *agentpb.PiWriteExecResult {
	if result.IsError {
		return &agentpb.PiWriteExecResult{Result: &agentpb.PiWriteExecResult_Error{Error: &agentpb.PiWriteExecError{Error: errorResultText(result, "写入文件失败")}}}
	}
	return &agentpb.PiWriteExecResult{Result: &agentpb.PiWriteExecResult_Success{Success: &agentpb.PiWriteExecSuccess{Output: partsText(result.Content)}}}
}

func buildPiGrepResumeResult(result NMessage) *agentpb.PiGrepExecResult {
	if result.IsError {
		return &agentpb.PiGrepExecResult{Result: &agentpb.PiGrepExecResult_Error{Error: &agentpb.PiGrepExecError{Error: errorResultText(result, "搜索文件失败")}}}
	}
	return &agentpb.PiGrepExecResult{Result: &agentpb.PiGrepExecResult_Success{Success: &agentpb.PiGrepExecSuccess{Output: partsText(result.Content)}}}
}

func buildPiFindResumeResult(result NMessage) *agentpb.PiFindExecResult {
	if result.IsError {
		return &agentpb.PiFindExecResult{Result: &agentpb.PiFindExecResult_Error{Error: &agentpb.PiFindExecError{Error: errorResultText(result, "查找文件失败")}}}
	}
	return &agentpb.PiFindExecResult{Result: &agentpb.PiFindExecResult_Success{Success: &agentpb.PiFindExecSuccess{Output: partsText(result.Content)}}}
}

func buildPiLsResumeResult(result NMessage) *agentpb.PiLsExecResult {
	if result.IsError {
		return &agentpb.PiLsExecResult{Result: &agentpb.PiLsExecResult_Error{Error: &agentpb.PiLsExecError{Error: errorResultText(result, "列出目录失败")}}}
	}
	return &agentpb.PiLsExecResult{Result: &agentpb.PiLsExecResult_Success{Success: &agentpb.PiLsExecSuccess{Output: partsText(result.Content)}}}
}
