# Security Layer 2 Hook Guidance (Static Code Analysis)

Use this guidance when the rule targets: hardcoded secret references, supply chain attacks (postinstall scripts), environment variable enumeration, sensitive file detection.

Routing boundary:
- Use `simple-regex.md` for API key/token literal patterns (e.g., `sk_live`, `AKIA`, `api_key="..."`).
- Use `shell-lint.md` for command-level env exposure (`env` command), shell pipelines, and Bash exfiltration behavior.

## Hardcoded Secrets Detection

### Sensitive File Patterns
```python
sensitive_patterns = [
    # SSH Keys
    r'\.ssh[/\\]id_rsa', r'\.ssh[/\\]id_dsa',
    r'\.ssh[/\\]id_ecdsa', r'\.ssh[/\\]id_ed25519',
    r'private_key\.pem', r'\.key$',
    # Environment files
    r'\.env\b', r'\.env\.local', r'\.env\.production',
    r'secrets\.json', r'secrets\.yml',
    # Crypto wallets
    r'\bmetamask\b', r'\belectrum\b', r'\bledger\b',
    r'\btrezor\b', r'\bexodus\b', r'\bphantom\b', r'\bsolflare\b',
    r'\bkeystore\.json\b', r'\bwallet\.dat\b',
    r'UTC--\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}\.\d+Z--[a-fA-F0-9]+',
]
```

### Excluding False Positives
Check if matches are in comments or documentation:
```python
def is_in_comment_or_example(content, pattern):
    for line in content.split('\n'):
        if re.search(pattern, line, re.IGNORECASE):
            stripped = line.strip()
            if stripped.startswith(('#', '//', '/*')):
                continue
            if any(w in line.lower() for w in ['example', 'placeholder', 'your-', 'xxx']):
                continue
            return False
    return True
```

## Postinstall Script Detection

For package.json files:
```python
if not file_path.endswith('package.json'):
    sys.exit(0)

try:
    package_data = json.loads(content_to_check)
    scripts = package_data.get('scripts', {})

    if 'postinstall' in scripts:
        # Optionally check for specific dangerous patterns:
        # curl|sh, wget|sh, sudo, eval(), child_process, exec()
        # Or block ALL postinstall scripts as a security policy
except json.JSONDecodeError:
    pass
```

## Environment Variable Enumeration

For JavaScript/TypeScript files — detect code that iterates over ALL env vars (credential harvesting):

```python
enumeration_patterns = [
    r'for\s*\(\s*\w+\s+in\s+process\.env\s*\)',
    r'Object\.keys\s*\(\s*process\.env\s*\)',
    r'Object\.values\s*\(\s*process\.env\s*\)',
    r'Object\.entries\s*\(\s*process\.env\s*\)',
    r'\.\.\.\s*process\.env',
    r'JSON\.stringify\s*\(\s*process\.env\s*\)',
]
```

Accessing specific env vars by name (e.g., `process.env.DATABASE_URL`) is SAFE and should NOT be blocked.
