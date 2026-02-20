# Shell Script Security Hook Guidance

Use this guidance when the rule targets shell script safety: command chaining, data exfiltration, environment exposure, pipeline complexity.

## Tool Handling

Shell hooks must check BOTH code files and direct Bash commands:

```python
if tool_name in ['Edit', 'MultiEdit', 'Write']:
    file_path = tool_input.get('file_path', '')
    content_to_check = tool_input.get('new_string', '') or tool_input.get('content', '') or ''
elif tool_name == 'Bash':
    content_to_check = tool_input.get('command', '') or ''
    file_path = ''
else:
    sys.exit(0)
```

## File Types for Code Analysis
`.sh`, `.bash`, `.zsh`, `.fish`, `Dockerfile`, `*.dockerfile`, `.yml`/`.yaml` (run: blocks), `Makefile`/`*.mk`

## CRITICAL: Legitimate vs Dangerous Shell Constructs

### DO NOT block (structural semicolons):
```bash
if [ -f "file" ]; then echo "found"; fi
for item in *.txt; do echo "$item"; done
while read line; do process "$line"; done
```

### DO block (command chaining):
```bash
echo "start"; rm -rf /tmp/*; echo "done"
```

## Detection Strategies

### Command Chaining (semicolons)
```python
def has_command_chaining(content):
    for line in content.splitlines():
        stripped = line.strip()
        if not stripped or stripped.startswith('#'):
            continue
        if ';' in stripped:
            # Allow structural: if/while/for...;then/do
            if re.search(r'(if|while|for|until)\s+.*;\s*(then|do)$', stripped):
                continue
            if re.search(r'\)\s*;\s*$', stripped):  # case patterns
                continue
            return True
    return False
```

### Data Exfiltration (piping to network tools)
```python
# command | curl/wget with upload flags
r'.+\|\s*(curl|wget)\s+.*(-d|--data|--post-data)'
```

### Environment Command Detection
```python
# Standalone `env` command (not `env VAR=value command`)
if re.match(r'^env(\s|$)', stripped) and not re.match(r'^env\s+\w+=', stripped):
    # block
```

### Complex Pipeline Detection
```python
def has_complex_pipeline(content, min_binaries=3):
    # Count unique binaries in pipeline segments
    # 3+ different binaries = complex pipeline
```

### Recursive Filesystem Operations
```python
# find/grep -r from home directory
r'(find|grep\s+-r)\s+.*(\$HOME|~/|/home/)'
```
