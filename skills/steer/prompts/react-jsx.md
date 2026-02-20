# React JSX Hook Guidance

Use this guidance when the rule targets React patterns: hooks usage, component patterns, data fetching, JSX syntax.

## The Challenge: JSX Parsing

React JSX cannot be reliably parsed by Python's `ast` module or simple regex. Two strategies:

### Strategy 1: Hybrid Python + Node.js (Most Accurate)

Generate a Python hook that embeds a Node.js analyzer script using `acorn` + `acorn-jsx` parsers:

```python
NODE_ANALYZER_SCRIPT = """
import { Parser } from 'https://esm.sh/acorn@8.11.3';
import jsx from 'https://esm.sh/acorn-jsx@5.3.2';
const JsxParser = Parser.extend(jsx());
// ... analysis logic
"""

# Execute with: node --experimental-network-imports analyzer.mjs
process = subprocess.run(
    ['node', '--experimental-network-imports', analyzer_path],
    input=content_to_check,
    capture_output=True, text=True, timeout=30
)
```

### Strategy 2: Regex Fallback (Simpler, Less Accurate)

For simpler rules, use regex with component detection:

```python
# Find React components (PascalCase function/const)
component_regex = r'(?:const|function)\s+([A-Z][a-zA-Z0-9]*)\s*'

# Find component body using brace counting (not regex!)
# Then search within the component body
```

## Common React Patterns

### Unsafe fetch in Component Body

`fetch()` should be inside `useEffect`, `useCallback`, or event handlers — not in the render body.

Detection approach:
1. Find React components (PascalCase names)
2. Find `useEffect`/`useCallback` callback bodies (safe zones)
3. Find all `fetch()` calls
4. Flag `fetch()` calls that are NOT inside safe zones

### Hook Rules
- Hooks must be called at the top level (not inside conditions/loops)
- Custom hooks must start with `use`

## File Filtering

Only check React files:
```python
if not file_path.endswith(('.jsx', '.tsx', '.js', '.ts')):
    sys.exit(0)
```

For `.js`/`.ts` files, optionally check if they contain JSX-like patterns before applying React-specific rules.

## TypeScript Handling

When using the Node.js parser, handle TypeScript by stripping type annotations if parsing fails:
```javascript
const jsCode = sourceCode
    .replace(/interface\s+\w+\s*{[^}]*}/g, '')  // Remove interfaces
    .replace(/:\s*[^=,;)\]]+(?=[=,;)\]])/g, '') // Remove type annotations
    .replace(/as\s+\w+/g, '')                    // Remove 'as' casts
```
