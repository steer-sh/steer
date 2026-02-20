# HTML Hook Guidance

Use this guidance when the rule targets HTML: accessibility, XSS prevention, semantic markup, form validation.

## File Filtering

Check HTML-related files including template frameworks:
```python
def is_html_file(path):
    p = path.lower()
    return p.endswith(('.html', '.htm', '.php', '.erb', '.vue', '.svelte', '.blade.php'))
```

## Security Patterns

### XSS Prevention - innerHTML
```python
innerHTML_patterns = [
    r'\.innerHTML\s*=\s*[^"\'\s][^;]*',  # innerHTML = variable
    r'\.innerHTML\s*=\s*.*\+.*',          # innerHTML = concatenation
    r'\.innerHTML\s*\+=',                  # innerHTML += anything
]
```

### Inline Event Handlers
```python
r'on(click|load|submit|change|focus|blur|keyup|keydown|mouseover)\s*=\s*["\']'
r'javascript:\s*'  # javascript: URLs
```

### Unsafe Link Targets
```python
# target="_blank" without rel="noopener"
blank_links = re.finditer(r'<a[^>]*target=["\']_blank["\'][^>]*>', content_to_check, re.IGNORECASE)
for match in blank_links:
    tag = match.group(0)
    if 'noopener' not in tag.lower():
        # block
```

## Accessibility Patterns

### Missing Alt Text
```python
img_tags = re.finditer(r'<img[^>]*>', content_to_check, re.IGNORECASE)
for match in img_tags:
    if 'alt=' not in match.group(0).lower():
        # block
```

### Form Inputs Without Labels
Check for `id` attribute on inputs and matching `<label for="...">`.

### Heading Hierarchy
Headings must not skip levels (h1 -> h3 without h2):
```python
headings = re.findall(r'<h([1-6])[^>]*>', content_to_check, re.IGNORECASE)
prev_level = 0
for level_str in headings:
    level = int(level_str)
    if prev_level > 0 and level > prev_level + 1:
        # block - skipped heading level
```

## Deprecated Tags
`<center>`, `<font>`, `<marquee>`, `<blink>` — suggest semantic HTML5 alternatives.

## Template Framework Awareness
For `.vue` files, check for `<template>` section. For `.svelte` files, check script/style ordering.
