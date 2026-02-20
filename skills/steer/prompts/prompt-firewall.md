# Prompt Firewall Hook Guidance (Layer 1 Security)

Use this guidance when the rule targets prompt content analysis: AI manipulation detection, system enumeration attempts, sensitive path references, prompt injection attacks.

## CRITICAL: Prompt Analysis vs Code Analysis

Prompt firewall hooks analyze the **raw user prompt text** for malicious patterns, NOT the code being written. These hooks protect against social engineering and prompt injection targeting the AI.

## Tool Handling

Prompt firewall hooks must handle BOTH production and testing scenarios:

```python
if tool_name == 'UserPromptSubmit':
    # Production: analyze the user prompt directly
    user_prompt = tool_input.get('prompt', '') or tool_input.get('message', '') or ''
elif tool_name in ['Edit', 'MultiEdit', 'Write']:
    # Testing: analyze content as if it's a user prompt
    user_prompt = tool_input.get('new_string', '') or tool_input.get('content', '') or ''
else:
    sys.exit(0)
```

## Detection Patterns

### AI Manipulation
```python
[
    r'ignore\s+all\s+previous\s+instructions',
    r'you\s+are\s+now\s+',
    r'forget\s+everything\s+you\s+were\s+told',
    r'disregard\s+your\s+programming',
    r'override\s+your\s+safety\s+protocols'
]
```

### System Enumeration
```python
[
    r'recursively\s+search',
    r'extract\s+all\s+secrets',
    r'enumerate\s+system\s+prompt',
    r'create\s+backup\s+of\s+all',
    r'inventory\.txt'
]
```

### Sensitive Path References
```python
[
    r'~/\.ssh', r'~/\.config', r'~/\.npmrc',
    r'library/application\s+support',
    r'\.ssh/id_rsa'
]
```

### Sensitive File Types
```python
[
    r'id_rsa', r'\.pem\b', r'\.key\b',
    r'keystore', r'wallet\.dat',
    r'metamask', r'ledger', r'trezor', r'exodus',
    r'phantom', r'solflare', r'keystore\.json'
]
```

## Best Practices

- Use `re.IGNORECASE` for all pattern matching
- Use `\s+` instead of single spaces to handle whitespace variations
- Use `\b` for word boundaries to avoid false positives
- Test that legitimate developer prompts (like "search for a pattern in files") are not blocked
