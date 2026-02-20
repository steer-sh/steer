# Python Security Hook Guidance

Use this guidance when the rule targets Python security concerns: subprocess/os.system command injection, SQL injection, eval/exec, dangerous imports.

## Detection Strategy: Always Use AST

For Python security rules, prefer `ast` module analysis over regex. AST catches structural patterns that regex misses.

```python
import ast

try:
    tree = ast.parse(content_to_check)
    visitor = SecurityVisitor()
    visitor.visit(tree)
except SyntaxError:
    sys.exit(0)  # Never block unparseable code
```

## Command Injection Prevention (CRITICAL)

This is the most nuanced security rule. The detection logic MUST follow this exact priority:

### Step 1: Handle os.system()
`os.system()` always uses a shell. Block if the argument is anything other than a static string literal.

### Step 2: Handle subprocess calls (run, Popen, check_output, check_call)

**The `shell=True` parameter is the gatekeeper.** Check for it FIRST, before analyzing arguments.

**Path A: `shell=True` is NOT present** → The call uses `shell=False` (default). This is the safest method. Arguments are passed directly to the program without shell interpretation. **ALLOW immediately.** Do not analyze further.

**Path B: `shell=True` IS present** → Now inspect the command argument:
- If the command is a static string literal → **ALLOW** (no user input possible)
- If the command uses `shlex.quote()` on all dynamic parts → **ALLOW** (safely escaped)
- If the command is anything else (f-string, variable, .format(), concatenation) → **BLOCK**

### shlex.quote() Safe Pattern (IMPORTANT - prevents false positives)

This is SAFE code that must NOT be blocked:
```python
# Pattern 1: Inline shlex.quote() in f-string
subprocess.run(f"git clone {shlex.quote(repo_url)}", shell=True, check=True)

# Pattern 2: Variable indirection (MOST COMMON in real code)
quoted_url = shlex.quote(repo_url)
subprocess.run(f"git clone {quoted_url}", shell=True, check=True)

# Pattern 3: Multi-step variable indirection
quoted_args = [shlex.quote(str(arg)) for arg in args]
full_command = f"{command} {' '.join(quoted_args)}"
subprocess.run(full_command, shell=True)  # full_command is a Name node, NOT a JoinedStr!
```

**Pattern 3 is the hardest to detect.** The subprocess argument is a variable (`ast.Name` node), not an f-string or a direct `shlex.quote()` call. Your hook MUST trace variables back to their assignments.

To detect safe quoting, check if:
1. The module `shlex` is imported
2. The argument is a direct `shlex.quote()` call → ALLOW
3. The argument is an f-string (JoinedStr) and shlex is imported → ALLOW
4. **The argument is a variable (Name node) and shlex is imported** → trace the variable's assignment in the same function scope; if it was built from shlex-quoted parts or an f-string with shlex context → ALLOW
5. The function name suggests safety (e.g., `_safe`, `_quoted`, `with_shell_safe`)

### Reference AST Visitor

```python
class CommandInjectionVisitor(ast.NodeVisitor):
    def __init__(self):
        self.violations = []
        self.has_shlex_import = False
        self.safe_vars = set()  # Track variables assigned from shlex.quote() or safe f-strings

    def visit_Import(self, node):
        for alias in node.names:
            if alias.name == 'shlex':
                self.has_shlex_import = True
        self.generic_visit(node)

    def visit_Assign(self, node):
        """Track variables assigned from shlex.quote() calls or safe f-strings"""
        if self.has_shlex_import:
            for target in node.targets:
                if isinstance(target, ast.Name):
                    if self._value_is_shlex_safe(node.value):
                        self.safe_vars.add(target.id)
        self.generic_visit(node)

    def _value_is_shlex_safe(self, node):
        """Check if a value comes from shlex.quote() or an f-string in shlex context"""
        # Direct shlex.quote() call
        if isinstance(node, ast.Call):
            if (isinstance(node.func, ast.Attribute) and
                isinstance(node.func.value, ast.Name) and
                node.func.value.id == 'shlex' and node.func.attr == 'quote'):
                return True
        # f-string when shlex is imported (conservative: assume safe quoting)
        if isinstance(node, ast.JoinedStr) and self.has_shlex_import:
            return True
        # List comprehension with shlex.quote inside
        if isinstance(node, ast.ListComp):
            elt = node.elt
            if self._value_is_shlex_safe(elt):
                return True
        return False

    def visit_Call(self, node):
        if not isinstance(node.func, ast.Attribute):
            self.generic_visit(node)
            return

        try:
            module_name = node.func.value.id
            func_name = node.func.attr
        except AttributeError:
            self.generic_visit(node)
            return

        # os.system: always uses shell
        if module_name == 'os' and func_name == 'system':
            if node.args and not self._is_static_string(node.args[0]):
                self.violations.append((node.lineno, "os.system() with dynamic input"))

        # subprocess calls
        if module_name == 'subprocess' and func_name in ('run', 'Popen', 'check_output', 'check_call'):
            # CRITICAL: Check shell=True FIRST
            shell_true = any(
                kw.arg == 'shell' and isinstance(kw.value, ast.Constant) and kw.value.value is True
                for kw in node.keywords
            )

            if not shell_true:
                # shell=False (default) - SAFE, stop analyzing
                self.generic_visit(node)
                return

            # shell=True found - now check the command argument
            if node.args:
                first_arg = node.args[0]
                if not self._is_static_string(first_arg) and not self._uses_shlex_quote(first_arg):
                    self.violations.append((node.lineno,
                        f"subprocess.{func_name}() with shell=True and dynamic input"))

        self.generic_visit(node)

    def _is_static_string(self, node):
        return isinstance(node, ast.Constant) and isinstance(node.value, str)

    def _uses_shlex_quote(self, node):
        """Check if the expression uses shlex.quote() for safe escaping"""
        # Direct shlex.quote() call
        if isinstance(node, ast.Call):
            if (isinstance(node.func, ast.Attribute) and
                isinstance(node.func.value, ast.Name) and
                node.func.value.id == 'shlex' and node.func.attr == 'quote'):
                return True
        # f-string when shlex is imported
        if isinstance(node, ast.JoinedStr) and self.has_shlex_import:
            return True
        # Variable that was previously assigned from a safe source
        if isinstance(node, ast.Name) and node.id in self.safe_vars:
            return True
        # Variable - if shlex is imported, check if it's likely safe
        if isinstance(node, ast.Name) and self.has_shlex_import:
            return node.id in self.safe_vars
        return False
```

## SQL Injection Prevention

Detect f-strings, %-formatting, and .format() calls containing SQL keywords.

```python
class SQLInjectionVisitor(ast.NodeVisitor):
    SQL_KEYWORDS = ['SELECT', 'INSERT', 'UPDATE', 'DELETE', 'DROP', 'CREATE', 'ALTER']

    def visit_JoinedStr(self, node):
        """Check f-strings for SQL keywords"""
        parts = []
        for value in node.values:
            if isinstance(value, ast.Constant):
                parts.append(str(value.value))
        text = ''.join(parts).upper()
        if any(kw in text for kw in self.SQL_KEYWORDS):
            self.violations.append((node.lineno, "f-string contains SQL keywords"))
        self.generic_visit(node)

    def visit_BinOp(self, node):
        """Check %-formatting for SQL keywords"""
        if isinstance(node.op, ast.Mod) and isinstance(node.left, ast.Constant):
            if isinstance(node.left.value, str):
                if any(kw in node.left.value.upper() for kw in self.SQL_KEYWORDS):
                    self.violations.append((node.lineno, "%-formatting with SQL keywords"))
        self.generic_visit(node)
```

## Dangerous Import Detection

```python
DANGEROUS_MODULES = ['pickle', 'marshal', 'shelve', 'dill']

# Check both `import pickle` and `from pickle import load`
```

## eval() / exec() Detection

```python
# Flag eval(), exec(), compile() calls
# These are always security-relevant in Python
class EvalVisitor(ast.NodeVisitor):
    def visit_Call(self, node):
        if isinstance(node.func, ast.Name) and node.func.id in ('eval', 'exec', 'compile'):
            self.violations.append((node.lineno, f"{node.func.id}() - code injection risk"))
        self.generic_visit(node)
```

## Python Version Compatibility

Always handle both AST node types:
- `ast.Constant` (Python 3.8+) — use `node.value`
- `ast.Str` / `ast.NameConstant` (older) — use `node.s` / `node.value`
