package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/steer-sh/steer-cli/internal/guard"
)

// HookFile describes an individual (non-master) hook file on disk.
type HookFile struct {
	Name     string
	FileName string
	Path     string
}

// HookFunction represents a parsed hook function with helper dependencies.
type HookFunction struct {
	Name            string
	OriginalName    string
	Code            string
	HelperFunctions []HelperFunction
	Constants       []ModuleConstant
}

// ModuleConstant is a top-level assignment in a hook file (e.g. a compiled
// regex) that main() or helpers reference. Constants must be carried into the
// master hook or those references fail at runtime — silently, because the
// master swallows hook exceptions.
type ModuleConstant struct {
	Name         string
	PrefixedName string
	Code         string
}

// HelperFunction represents a helper function within a hook.
type HelperFunction struct {
	Name         string
	PrefixedName string
	Code         string
}

// ListIndividualHooks returns all individual .py hooks excluding steer-hooks.py.
func ListIndividualHooks(scope string) ([]HookFile, error) {
	hooksDir, err := GetHooksDir(scope)
	if err != nil {
		return nil, err
	}

	if _, err := os.Stat(hooksDir); os.IsNotExist(err) {
		return []HookFile{}, nil
	}

	entries, err := os.ReadDir(hooksDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read hooks directory: %w", err)
	}

	hooks := make([]HookFile, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.HasSuffix(name, ".py") || name == MasterHookName {
			continue
		}

		hooks = append(hooks, HookFile{
			Name:     strings.TrimSuffix(name, ".py"),
			FileName: name,
			Path:     filepath.Join(hooksDir, name),
		})
	}

	sort.Slice(hooks, func(i, j int) bool {
		return hooks[i].Name < hooks[j].Name
	})

	return hooks, nil
}

// ResolveHook finds a hook by full path, file name, stem, or readable name.
func ResolveHook(scope, identifier string) (HookFile, error) {
	hooks, err := ListIndividualHooks(scope)
	if err != nil {
		return HookFile{}, err
	}
	if len(hooks) == 0 {
		return HookFile{}, fmt.Errorf("no hooks found")
	}

	id := strings.TrimSpace(identifier)
	if id == "" {
		return HookFile{}, fmt.Errorf("hook identifier cannot be empty")
	}

	idAbs := ""
	if abs, err := filepath.Abs(id); err == nil {
		idAbs = abs
	}

	for _, hook := range hooks {
		if id == hook.Path || id == hook.FileName || id == hook.Name {
			return hook, nil
		}

		readable := strings.ReplaceAll(hook.Name, "-", " ")
		if strings.EqualFold(readable, id) {
			return hook, nil
		}

		if idAbs != "" {
			hookAbs, err := filepath.Abs(hook.Path)
			if err == nil && hookAbs == idAbs {
				return hook, nil
			}
		}
	}

	return HookFile{}, fmt.Errorf("hook not found: %s", identifier)
}

// ConsolidateHooks rebuilds the master steer-hooks.py from all individual hooks.
func ConsolidateHooks(scope string) error {
	if err := EnsureDirectories(scope); err != nil {
		return err
	}

	hooks, err := ListIndividualHooks(scope)
	if err != nil {
		return fmt.Errorf("failed to list hooks: %w", err)
	}

	masterPath, err := GetMasterHookPath(scope)
	if err != nil {
		return err
	}

	if len(hooks) == 0 {
		if err := os.Remove(masterPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove master hook: %w", err)
		}
		return nil
	}

	manifest, err := LoadManifest(scope)
	if err != nil {
		return fmt.Errorf("failed to load manifest: %w", err)
	}

	hookFunctions := make([]HookFunction, 0, len(hooks))
	importSet := map[string]struct{}{}
	for _, line := range guard.RequiredImports() {
		importSet[line] = struct{}{}
	}

	hookMeta := make(map[string]map[string]string, len(hooks))

	for _, hook := range hooks {
		hookFunc, imports, err := parseHookFile(hook.Path)
		if err != nil {
			return fmt.Errorf("failed to parse hook %s: %w", hook.Path, err)
		}
		hookFunctions = append(hookFunctions, hookFunc)

		rule := ""
		source := ""
		sourceID := ""
		if entry, ok := manifest.Hooks[hook.Name]; ok {
			rule = strings.TrimSpace(entry.Rule)
			source = strings.TrimSpace(entry.Source)
			sourceID = strings.TrimSpace(entry.SourceID)
		}
		hookMeta[hookFunc.Name] = map[string]string{
			"hook_name": hook.Name,
			"rule":      rule,
			"source":    source,
			"source_id": sourceID,
		}

		for _, line := range imports {
			importSet[line] = struct{}{}
		}
	}

	importLines := make([]string, 0, len(importSet))
	for line := range importSet {
		importLines = append(importLines, line)
	}
	sort.Strings(importLines)

	masterContent := generateMasterHook(hookFunctions, importLines, hookMeta)
	if err := os.WriteFile(masterPath, []byte(masterContent), 0o755); err != nil {
		return fmt.Errorf("failed to write master hook file: %w", err)
	}

	return nil
}

func parseHookFile(filePath string) (HookFunction, []string, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return HookFunction{}, nil, err
	}

	contentStr := string(content)
	filename := filepath.Base(filePath)
	funcName := sanitizeFunctionName(filename)

	mainFunc, err := extractMainFunction(contentStr)
	if err != nil {
		return HookFunction{}, nil, fmt.Errorf("failed to extract main() from %s: %w", filename, err)
	}

	helpers := extractHelperFunctions(contentStr, funcName)
	constants := extractModuleConstants(contentStr, funcName)

	// Update helper and constant references in helper bodies, constant
	// bodies, and the main body.
	for i := range helpers {
		updated := helpers[i].Code
		for _, helper := range helpers {
			updated = replaceFunctionCalls(updated, helper.Name, helper.PrefixedName)
		}
		for _, c := range constants {
			updated = replaceIdentifier(updated, c.Name, c.PrefixedName)
		}
		helpers[i].Code = updated
	}

	for i := range constants {
		updated := constants[i].Code
		for _, c := range constants {
			updated = replaceIdentifier(updated, c.Name, c.PrefixedName)
		}
		constants[i].Code = updated
	}

	updatedMain := mainFunc
	for _, helper := range helpers {
		updatedMain = replaceFunctionCalls(updatedMain, helper.Name, helper.PrefixedName)
	}
	for _, c := range constants {
		updatedMain = replaceIdentifier(updatedMain, c.Name, c.PrefixedName)
	}

	return HookFunction{
		Name:            funcName,
		OriginalName:    filename,
		Code:            updatedMain,
		HelperFunctions: helpers,
		Constants:       constants,
	}, extractImports(contentStr), nil
}

func extractImports(content string) []string {
	lines := strings.Split(content, "\n")
	imports := make([]string, 0)
	seen := map[string]struct{}{}

	for _, line := range lines {
		trimmedRight := strings.TrimRight(line, " \t")
		if strings.HasPrefix(trimmedRight, "import ") || strings.HasPrefix(trimmedRight, "from ") {
			if _, ok := seen[trimmedRight]; ok {
				continue
			}
			seen[trimmedRight] = struct{}{}
			imports = append(imports, trimmedRight)
		}
	}

	return imports
}

func sanitizeFunctionName(filename string) string {
	name := strings.TrimSuffix(filename, ".py")
	name = strings.ReplaceAll(name, "-", "_")
	name = strings.TrimRight(name, "_.")

	if len(name) == 0 {
		return "hook_function"
	}

	if !regexp.MustCompile(`^[a-zA-Z_]`).MatchString(name) {
		name = "_" + name
	}

	reg := regexp.MustCompile(`[^a-zA-Z0-9_]`)
	name = reg.ReplaceAllString(name, "_")

	reg = regexp.MustCompile(`_{2,}`)
	name = reg.ReplaceAllString(name, "_")

	if name == "" {
		name = "hook_function"
	}

	return name
}

func extractMainFunction(content string) (string, error) {
	lines := strings.Split(content, "\n")
	start := -1

	for i, line := range lines {
		if strings.TrimSpace(line) == "def main():" && isTopLevel(line) {
			start = i
			break
		}
	}

	if start == -1 {
		return "", fmt.Errorf("main function not found")
	}

	body := make([]string, 0)
	for i := start + 1; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		if isTopLevel(line) && (strings.HasPrefix(trimmed, "def ") || strings.HasPrefix(trimmed, "if __name__")) {
			break
		}

		if strings.HasPrefix(line, "    ") {
			body = append(body, line[4:])
		} else if strings.HasPrefix(line, "\t") {
			body = append(body, line[1:])
		} else {
			body = append(body, line)
		}
	}

	return strings.Join(body, "\n"), nil
}

func extractHelperFunctions(content, hookPrefix string) []HelperFunction {
	lines := strings.Split(content, "\n")
	helpers := make([]HelperFunction, 0)
	funcRegex := regexp.MustCompile(`^def\s+([a-zA-Z_][a-zA-Z0-9_]*)\([^)]*\):`)

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if !isTopLevel(line) {
			continue
		}

		match := funcRegex.FindStringSubmatch(strings.TrimSpace(line))
		if len(match) < 2 {
			continue
		}

		name := match[1]
		if name == "main" {
			continue
		}

		funcCode, end := extractFunctionCode(lines, i)
		i = end - 1

		prefixed := hookPrefix + "_" + name
		funcCode = replaceFunctionDefinitionName(funcCode, name, prefixed)
		helpers = append(helpers, HelperFunction{
			Name:         name,
			PrefixedName: prefixed,
			Code:         funcCode,
		})
	}

	return helpers
}

func extractFunctionCode(lines []string, start int) (string, int) {
	funcLines := make([]string, 0)
	end := len(lines)

	for i := start; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		if i > start && isTopLevel(line) && (strings.HasPrefix(trimmed, "def ") || strings.HasPrefix(trimmed, "if __name__")) {
			end = i
			break
		}

		funcLines = append(funcLines, line)
	}

	return strings.Join(funcLines, "\n"), end
}

func replaceFunctionDefinitionName(code, oldName, newName string) string {
	lines := strings.Split(code, "\n")
	if len(lines) == 0 {
		return code
	}

	defRegex := regexp.MustCompile(`^def\s+` + regexp.QuoteMeta(oldName) + `\b`)
	lines[0] = defRegex.ReplaceAllString(lines[0], "def "+newName)
	return strings.Join(lines, "\n")
}

func replaceFunctionCalls(code, oldName, newName string) string {
	callRegex := regexp.MustCompile(`\b` + regexp.QuoteMeta(oldName) + `\s*\(`)
	return callRegex.ReplaceAllString(code, newName+"(")
}

func replaceIdentifier(code, oldName, newName string) string {
	idRegex := regexp.MustCompile(`\b` + regexp.QuoteMeta(oldName) + `\b`)
	return idRegex.ReplaceAllString(code, newName)
}

// extractModuleConstants collects top-level assignment statements (constants,
// compiled regexes, lookup tables) so they survive consolidation. Statements
// spanning multiple lines are consumed with a string-aware bracket scanner.
func extractModuleConstants(content, hookPrefix string) []ModuleConstant {
	lines := strings.Split(content, "\n")
	assignRegex := regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s*=(?:[^=]|$)`)

	constants := make([]ModuleConstant, 0)
	var docState pyLineState
	for i := 0; i < len(lines); i++ {
		line := lines[i]

		// Track module-level (doc)strings so their content is never
		// mistaken for an assignment.
		if docState.inTriple {
			scanPythonLine(line, &docState)
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, `"""`) || strings.HasPrefix(trimmed, "'''") {
			scanPythonLine(line, &docState)
			continue
		}

		if !isTopLevel(line) {
			continue
		}

		// Skip block statements: their bodies are indented and therefore
		// non-top-level, but the header line itself must not be scanned.
		if strings.HasPrefix(trimmed, "def ") || strings.HasPrefix(trimmed, "class ") ||
			strings.HasPrefix(trimmed, "if ") || strings.HasPrefix(trimmed, "@") ||
			strings.HasPrefix(trimmed, "try") || strings.HasPrefix(trimmed, "with ") ||
			strings.HasPrefix(trimmed, "for ") || strings.HasPrefix(trimmed, "while ") {
			continue
		}

		match := assignRegex.FindStringSubmatch(line)
		if match == nil {
			continue
		}

		stmt := []string{line}
		var state pyLineState
		depth := scanPythonLine(line, &state)
		for (depth > 0 || state.inTriple) && i+1 < len(lines) {
			i++
			stmt = append(stmt, lines[i])
			depth += scanPythonLine(lines[i], &state)
		}

		constants = append(constants, ModuleConstant{
			Name:         match[1],
			PrefixedName: hookPrefix + "_" + match[1],
			Code:         strings.Join(stmt, "\n"),
		})
	}

	return constants
}

// pyLineState carries multi-line string state across scanPythonLine calls.
type pyLineState struct {
	inTriple bool
	tripleCh byte
}

// scanPythonLine returns the bracket-depth delta of one line, ignoring
// brackets inside string literals and comments. Good enough for statement
// boundary detection; not a full tokenizer.
func scanPythonLine(line string, state *pyLineState) int {
	depth := 0
	for i := 0; i < len(line); {
		c := line[i]

		if state.inTriple {
			if c == state.tripleCh && strings.HasPrefix(line[i:], strings.Repeat(string(state.tripleCh), 3)) {
				state.inTriple = false
				i += 3
				continue
			}
			i++
			continue
		}

		switch c {
		case '\'', '"':
			if strings.HasPrefix(line[i:], strings.Repeat(string(c), 3)) {
				state.inTriple = true
				state.tripleCh = c
				i += 3
				continue
			}
			j := i + 1
			for j < len(line) {
				if line[j] == '\\' {
					j += 2
					continue
				}
				if line[j] == c {
					break
				}
				j++
			}
			i = j + 1
		case '#':
			return depth
		case '(', '[', '{':
			depth++
			i++
		case ')', ']', '}':
			depth--
			i++
		default:
			i++
		}
	}
	return depth
}

func isTopLevel(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	return len(line) == len(strings.TrimLeft(line, " \t"))
}

func generateMasterHook(hookFunctions []HookFunction, importLines []string, hookMeta map[string]map[string]string) string {
	var builder strings.Builder

	builder.WriteString(`#!/usr/bin/env python3
"""
Master steer hook - consolidated from individual hooks.
Generated automatically - do not edit manually.
"""

`)

	for _, imp := range importLines {
		builder.WriteString(imp)
		builder.WriteString("\n")
	}
	builder.WriteString("\n")

	// A JSON object with string keys/values is also a valid Python dict literal.
	metaJSON, err := json.MarshalIndent(hookMeta, "", "    ")
	if err != nil {
		metaJSON = []byte("{}")
	}
	builder.WriteString("STEER_HOOK_META = ")
	builder.Write(metaJSON)
	builder.WriteString("\n\n")
	builder.WriteString(guard.RuntimeSource())
	builder.WriteString("\n")

	for _, hookFunc := range hookFunctions {
		for _, constant := range hookFunc.Constants {
			builder.WriteString(constant.Code)
			builder.WriteString("\n\n")
		}
	}

	for _, hookFunc := range hookFunctions {
		for _, helper := range hookFunc.HelperFunctions {
			builder.WriteString(helper.Code)
			builder.WriteString("\n\n")
		}
	}

	for _, hookFunc := range hookFunctions {
		builder.WriteString(fmt.Sprintf("def %s():\n", hookFunc.Name))
		builder.WriteString("    \"\"\"\n")
		builder.WriteString(fmt.Sprintf("    Hook function from: %s\n", hookFunc.OriginalName))
		builder.WriteString("    \"\"\"\n")

		for _, line := range strings.Split(hookFunc.Code, "\n") {
			if strings.TrimSpace(line) == "" {
				builder.WriteString("\n")
				continue
			}
			builder.WriteString("    ")
			builder.WriteString(line)
			builder.WriteString("\n")
		}
		builder.WriteString("\n")
	}

	builder.WriteString("def main():\n")
	builder.WriteString("    steer_run([\n")
	for _, hookFunc := range hookFunctions {
		builder.WriteString(fmt.Sprintf("        %s,\n", hookFunc.Name))
	}
	builder.WriteString("    ])\n\n")
	builder.WriteString("if __name__ == \"__main__\":\n")
	builder.WriteString("    main()\n")

	return builder.String()
}
