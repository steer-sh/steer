# Python Security Hook Guidance

Use this guidance when the rule targets Python security concerns: subprocess/os.system command injection, SQL injection, Python `eval`/`exec`/`compile`, dangerous imports.

Route JavaScript/TypeScript `eval()` rules to `simple-regex.md`.

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

**Pattern 3 is the hardest to detect.** Two things make it hard:
- The subprocess argument is a variable (`ast.Name` node), not an f-string or a direct `shlex.quote()` call. Your hook MUST trace variables back to their assignments.
- The leading `{command}` token is a bare, unprovable variable — the program name, chosen by the caller, not argument data. A visitor that demands proof for **every** interpolated value blocks Pattern 3 and produces a false positive.

**Leading-token trust decision:** a bare variable is permitted in the *leading* position of the command string only, and only when the string contains at least one other dynamic part and every one of those parts is proven quoted. A lone bare variable (`f"{cmd}"`) or a bare variable followed by unproven parts (`f"{cmd} {user_input}"`) must still BLOCK. Do NOT widen this into "any f-string is fine if shlex is imported somewhere in the file" — a file-level import proves nothing about an individual call site.

To detect safe quoting, check if:
1. The argument is a direct `shlex.quote()` call (or `from shlex import quote` alias) → ALLOW
2. The argument is an f-string (JoinedStr) where **every interpolated value** is proven safe (quoted with `shlex.quote()` or traced to a previously proven-safe variable), applying the leading-token exception above → ALLOW
3. **The argument is a variable (Name node)** → trace the variable assignment in the same function scope; allow only if it was built from safe quoted parts
4. If safety cannot be proven structurally, **BLOCK**
5. Never treat naming hints (e.g., `_safe`, `_quoted`) or file-level imports as proof of safety
6. **A proof describes one value, so rebinding the name ends it.** Forget the proof on every rebinding: plain, tuple, starred, `for`, `with`, walrus, augmented and annotated targets, `del`, parameters, `except ... as` names, imports, `def` and `class` names, and `match` captures. Keeping one proof across `x += "'"` is also wrong: a static quote character changes the quoting of every later fragment. Judge the value before binding the target, so `x = f"ls {x}"` reads the old proof. Test this through `subprocess.run(f"ls {x}", shell=True)`: `os.system(f"ls {x}")` blocks every non-literal argument whatever the proof says, so it cannot expose a stale proof.

### Reference AST Visitor

```python
class CommandInjectionVisitor(ast.NodeVisitor):
    def __init__(self):
        self.violations = []
        self.safe_vars = set()  # Variables proven to contain shell-safe fragments
        self.literal_string_vars = set()  # Variables assigned static string literals
        self.shlex_aliases = {'shlex'}  # import shlex as sx -> track sx
        self.quote_aliases = set()  # from shlex import quote as q -> track q

    def visit_Import(self, node):
        for alias in node.names:
            self._forget(alias.asname or alias.name.split('.')[0])
            if alias.name == 'shlex':
                self.shlex_aliases.add(alias.asname or alias.name)
        self.generic_visit(node)

    def visit_ImportFrom(self, node):
        for alias in node.names:
            self._forget(alias.asname or alias.name)
        if node.module == 'shlex':
            for alias in node.names:
                if alias.name == 'quote':
                    self.quote_aliases.add(alias.asname or alias.name)
        self.generic_visit(node)

    def _forget(self, name):
        self.safe_vars.discard(name)
        self.literal_string_vars.discard(name)

    def visit_Name(self, node):
        # Any store or delete of a name ends what was proven about its old value
        if isinstance(node.ctx, (ast.Store, ast.Del)):
            self._forget(node.id)

    def visit_arg(self, node):
        self._forget(node.arg)
        self.generic_visit(node)

    def _visit_named_binding(self, node):
        # These nodes bind a name held as a string, not as a Name node
        if node.name:
            self._forget(node.name)
        self.generic_visit(node)

    visit_ExceptHandler = _visit_named_binding
    visit_MatchAs = _visit_named_binding
    visit_MatchStar = _visit_named_binding

    def _visit_definition(self, node):
        # Decorators and defaults run before the name binds; forgetting last also ends any proof the body made
        self.generic_visit(node)
        self._forget(node.name)

    visit_FunctionDef = _visit_definition
    visit_AsyncFunctionDef = _visit_definition
    visit_ClassDef = _visit_definition

    def visit_MatchMapping(self, node):
        if node.rest:
            self._forget(node.rest)
        self.generic_visit(node)

    def visit_Assign(self, node):
        """Track variables assigned from safe shell expressions."""
        # The value runs before the targets bind, so it is visited and judged first
        self.visit(node.value)
        value_is_safe = self._value_is_shlex_safe(node.value)
        value_is_static = self._is_static_string(node.value)
        for target in node.targets:
            self.visit(target)
            if isinstance(target, ast.Name):
                if value_is_safe:
                    self.safe_vars.add(target.id)
                if value_is_static:
                    self.literal_string_vars.add(target.id)

    def visit_AugAssign(self, node):
        if not isinstance(node.target, ast.Name):
            # A subscript or attribute target is evaluated before the value
            self.generic_visit(node)
            return
        self.visit(node.value)
        self.visit(node.target)

    def visit_NamedExpr(self, node):
        self.visit(node.value)
        self.visit(node.target)

    def visit_AnnAssign(self, node):
        # A bare annotation binds nothing; otherwise the target binds before it runs
        if node.value is not None:
            self.visit(node.value)
        if node.value is not None or not isinstance(node.target, ast.Name):
            self.visit(node.target)
        self.visit(node.annotation)

    def _is_shlex_quote_call(self, node):
        if isinstance(node, ast.Call):
            # shlex.quote(x) with module alias support
            if (
                isinstance(node.func, ast.Attribute)
                and isinstance(node.func.value, ast.Name)
                and node.func.value.id in self.shlex_aliases
                and node.func.attr == 'quote'
            ):
                return True
            # quote(x) from "from shlex import quote" (with alias support)
            if isinstance(node.func, ast.Name) and node.func.id in self.quote_aliases:
                return True
        return False

    def _formatted_value_is_safe(self, formatted_value):
        """Allow only proven-safe dynamic fragments inside f-strings."""
        expr = formatted_value.value
        if self._is_shlex_quote_call(expr):
            return True
        if isinstance(expr, ast.Name) and (
            expr.id in self.safe_vars or expr.id in self.literal_string_vars
        ):
            return True
        # Allow " ".join(quoted_args) if quoted_args was already proven safe
        if (
            isinstance(expr, ast.Call)
            and isinstance(expr.func, ast.Attribute)
            and expr.func.attr == 'join'
            and all(isinstance(arg, ast.Name) and arg.id in self.safe_vars for arg in expr.args)
        ):
            return True
        return False

    def _joinedstr_is_shlex_safe(self, node):
        has_dynamic = False
        formatted = [v for v in node.values if isinstance(v, ast.FormattedValue)]
        for value in node.values:
            if isinstance(value, ast.FormattedValue):
                has_dynamic = True
                if not self._formatted_value_is_safe(value):
                    # Leading bare-Name token is the program name (caller-chosen,
                    # not argument data). Permit it only when the string has other
                    # dynamic parts and every one of them is proven safe.
                    if (
                        value is node.values[0]
                        and isinstance(value.value, ast.Name)
                        and len(formatted) > 1
                        and all(self._formatted_value_is_safe(f) for f in formatted[1:])
                    ):
                        continue
                    return False
        return has_dynamic

    def _value_is_shlex_safe(self, node):
        """Check if a value is provably shell-safe."""
        if self._is_shlex_quote_call(node):
            return True
        if isinstance(node, ast.JoinedStr):
            return self._joinedstr_is_shlex_safe(node)
        if isinstance(node, ast.Name):
            return node.id in self.safe_vars
        if isinstance(node, ast.ListComp):
            return self._value_is_shlex_safe(node.elt)
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
        """Check if the expression is structurally proven safe."""
        return self._value_is_shlex_safe(node)
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

    def visit_Call(self, node):
        """.format() on SQL string literals"""
        if (isinstance(node.func, ast.Attribute)
                and node.func.attr == 'format'
                and isinstance(node.func.value, ast.Constant)
                and isinstance(node.func.value.value, str)):
            if any(kw in node.func.value.value.upper() for kw in self.SQL_KEYWORDS):
                self.violations.append((node.lineno, ".format() with SQL keywords"))
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
