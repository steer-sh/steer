package claude

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
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

	hookFunctions := make([]HookFunction, 0, len(hooks))
	importSet := map[string]struct{}{
		"import io":   {},
		"import json": {},
		"import sys":  {},
	}

	for _, hook := range hooks {
		hookFunc, imports, err := parseHookFile(hook.Path)
		if err != nil {
			return fmt.Errorf("failed to parse hook %s: %w", hook.Path, err)
		}
		hookFunctions = append(hookFunctions, hookFunc)

		for _, line := range imports {
			importSet[line] = struct{}{}
		}
	}

	importLines := make([]string, 0, len(importSet))
	for line := range importSet {
		importLines = append(importLines, line)
	}
	sort.Strings(importLines)

	masterContent := generateMasterHook(hookFunctions, importLines)
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

	// Update helper references in helper bodies and in main body.
	for i := range helpers {
		updated := helpers[i].Code
		for _, helper := range helpers {
			updated = replaceFunctionCalls(updated, helper.Name, helper.PrefixedName)
		}
		helpers[i].Code = updated
	}

	updatedMain := mainFunc
	for _, helper := range helpers {
		updatedMain = replaceFunctionCalls(updatedMain, helper.Name, helper.PrefixedName)
	}

	return HookFunction{
		Name:            funcName,
		OriginalName:    filename,
		Code:            updatedMain,
		HelperFunctions: helpers,
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

func isTopLevel(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	return len(line) == len(strings.TrimLeft(line, " \t"))
}

func generateMasterHook(hookFunctions []HookFunction, importLines []string) string {
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
	builder.WriteString("    stdin_data = sys.stdin.read()\n\n")
	builder.WriteString("    hooks_to_run = [\n")
	for _, hookFunc := range hookFunctions {
		builder.WriteString(fmt.Sprintf("        %s,\n", hookFunc.Name))
	}
	builder.WriteString("    ]\n\n")
	builder.WriteString("    for hook_func in hooks_to_run:\n")
	builder.WriteString("        captured = io.StringIO()\n")
	builder.WriteString("        old_stdout = sys.stdout\n")
	builder.WriteString("        old_stdin = sys.stdin\n")
	builder.WriteString("        sys.stdout = captured\n")
	builder.WriteString("        sys.stdin = io.StringIO(stdin_data)\n\n")
	builder.WriteString("        try:\n")
	builder.WriteString("            hook_func()\n")
	builder.WriteString("        except SystemExit:\n")
	builder.WriteString("            pass\n")
	builder.WriteString("        except Exception:\n")
	builder.WriteString("            pass\n")
	builder.WriteString("        finally:\n")
	builder.WriteString("            sys.stdout = old_stdout\n")
	builder.WriteString("            sys.stdin = old_stdin\n\n")
	builder.WriteString("        output = captured.getvalue().strip()\n")
	builder.WriteString("        if output:\n")
	builder.WriteString("            blocked = False\n")
	builder.WriteString("            try:\n")
	builder.WriteString("                result = json.loads(output)\n")
	builder.WriteString("                blocked = isinstance(result, dict) and result.get(\"decision\") == \"block\"\n")
	builder.WriteString("            except json.JSONDecodeError:\n")
	builder.WriteString("                compact_output = \"\".join(output.split())\n")
	builder.WriteString("                blocked = \"\\\"decision\\\":\\\"block\\\"\" in compact_output\n\n")
	builder.WriteString("            if blocked:\n")
	builder.WriteString("                print(output)\n")
	builder.WriteString("                sys.exit(0)\n\n")
	builder.WriteString("    sys.exit(0)\n\n")
	builder.WriteString("if __name__ == \"__main__\":\n")
	builder.WriteString("    main()\n")

	return builder.String()
}
