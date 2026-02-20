# Simple Regex Hook Guidance

Use this guidance when the rule targets simple keyword or pattern blocking: console.log, eval(), TODO/FIXME comments, var declarations, debugger statements, API key detection.

## Core Patterns

### Keyword Blocking (console.log, debugger, etc.)
```python
if re.search(r'console\.log\s*\(', content_to_check):
    # block
```

### Comment-Aware Detection (TODO, FIXME, HACK)

When blocking keywords that appear in comments, match the comment marker followed by the keyword:

```python
forbidden_keywords = r'\b(TODO|FIXME|HACK)\b'
comment_pattern = re.compile(
    r'(//|#|/\*).*?' + forbidden_keywords,
    re.IGNORECASE | re.DOTALL
)
```

### Production-Only Rules

For rules that only apply in production paths:
```python
production_patterns = [
    r'[/\\]prod[/\\]', r'[/\\]production[/\\]',
    r'[/\\]dist[/\\]', r'[/\\]build[/\\]', r'[/\\]release[/\\]'
]
is_production = any(re.search(p, file_path, re.IGNORECASE) for p in production_patterns)
if not is_production:
    sys.exit(0)
```

### var Declaration Blocking (JavaScript)

Must strip comments and strings first to avoid false positives:
```python
def strip_comments_and_strings(code):
    code = re.sub(r'//.*?$', '', code, flags=re.MULTILINE)
    code = re.sub(r'/\*.*?\*/', '', code, flags=re.DOTALL)
    code = re.sub(r'"[^"]*"', '""', code)
    code = re.sub(r"'[^']*'", "''", code)
    code = re.sub(r'`[^`]*`', '``', code)
    return code

clean = strip_comments_and_strings(content_to_check)
if re.search(r'\bvar\s+[a-zA-Z_$][a-zA-Z0-9_$]*', clean):
    # block
```

### API Key / Hardcoded Secret Detection

Exclude secure patterns first to avoid false positives:
```python
secure_patterns = [
    r'process\.env\.\w+', r'os\.getenv\(', r'os\.environ\.',
    r'config\.\w+', r'settings\.\w+',
    r'your-\w+-here', r'XXXXXXX', r'example\.com', r'placeholder',
]
is_secure = any(re.search(p, content_to_check, re.IGNORECASE) for p in secure_patterns)

if not is_secure:
    api_key_patterns = [
        r'api_key\s*=\s*["\'][^"\']+["\']',
        r'secret\s*=\s*["\'][^"\']+["\']',
        r'sk_live_[a-zA-Z0-9]{24,}',  # Stripe
        r'AKIA[0-9A-Z]{16}',  # AWS
    ]
```

## Context-Aware Matching

When blocking keywords like `var` or `eval`, ensure you match them as code, not inside strings or comments. Use the "Sanitize First, Then Search" approach:
1. Strip comments and string literals
2. Run your regex on the cleaned code

## File Type Filtering

If the rule is language-specific, always filter by file extension:
```python
if not (file_path.endswith('.js') or file_path.endswith('.ts')):
    sys.exit(0)
```

## ReDoS Prevention

Never use nested quantifiers:
- BAD: `(a*)*`, `(a|b)*`, `([^{}]*\{[^{}]*\})*`
- GOOD: `[^{}]*`, `\b\w+\b`, simple alternation without nesting
