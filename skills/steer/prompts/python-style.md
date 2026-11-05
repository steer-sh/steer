# Python Style Hook Guidance

Use this guidance when the rule targets Python code style: naming conventions, docstrings, mutable defaults, import organization.

## Detection Strategy: Use AST

Python style rules require structural analysis. Always use `ast` module.

## Naming Conventions

### Rules
- **Class names**: PascalCase (`MyClass`, `UserManager`)
- **Function/method names**: snake_case (`my_function`, `process_data`)
- **Module constants**: ALL_CAPS (`MAX_SIZE`, `DEFAULT_TIMEOUT`)
- **Module variables**: snake_case (`user_count`, `is_active`)

### CRITICAL: What NOT to flag
- **Private methods**: `_helper_method`, `__private_method` — these are VALID
- **Dunder methods**: `__init__`, `__str__`, `__repr__` — these are VALID
- **Built-in overrides**: `__len__`, `__getitem__` — these are VALID

```python
class NamingVisitor(ast.NodeVisitor):
    def __init__(self):
        self.violations = []

    def visit_ClassDef(self, node):
        if not self._is_pascal_case(node.name):
            self.violations.append((node.lineno, f"Class '{node.name}' should be PascalCase"))
        self.generic_visit(node)

    def visit_FunctionDef(self, node):
        self._check_function_name(node)
        self.generic_visit(node)

    def visit_AsyncFunctionDef(self, node):
        self._check_function_name(node)
        self.generic_visit(node)

    def visit_Module(self, node):
        for stmt in node.body:
            self._check_module_assignment(stmt)
        self.generic_visit(node)

    def _check_function_name(self, node):
        # Skip dunder and private methods/functions
        if node.name.startswith('__') and node.name.endswith('__'):
            return
        if node.name.startswith('_'):
            return
        if not self._is_snake_case(node.name):
            self.violations.append((node.lineno, f"Function '{node.name}' should be snake_case"))

    def _check_module_assignment(self, stmt):
        # Module-level constants vs variables
        if isinstance(stmt, ast.Assign):
            targets = stmt.targets
            value = stmt.value
            lineno = stmt.lineno
        elif isinstance(stmt, ast.AnnAssign):
            targets = [stmt.target]
            value = stmt.value
            lineno = stmt.lineno
        else:
            return

        for target in targets:
            if not isinstance(target, ast.Name):
                continue
            name = target.id
            # Module dunder metadata (e.g., __all__, __version__) is conventional.
            if name.startswith('__') and name.endswith('__'):
                continue
            if self._is_constant_candidate(value):
                if not self._is_all_caps(name):
                    self.violations.append((lineno, f"Module constant '{name}' should be ALL_CAPS"))
            else:
                if not self._is_snake_case(name):
                    self.violations.append((lineno, f"Module variable '{name}' should be snake_case"))

    def _is_snake_case(self, name):
        return name.islower() and name.replace('_', '').isalnum()

    def _is_pascal_case(self, name):
        return name[0].isupper() and '_' not in name and name.isalnum()

    def _is_all_caps(self, name):
        return name.upper() == name and any(ch.isalpha() for ch in name)

    def _is_constant_candidate(self, value):
        # Heuristic: treat literal-like module assignments as constants.
        # Tune this for project conventions.
        if value is None:
            return False
        return isinstance(value, (ast.Constant, ast.Tuple, ast.Set, ast.Dict, ast.List))
```

## Docstring Enforcement

```python
class DocstringVisitor(ast.NodeVisitor):
    def _has_docstring(self, node):
        if (node.body and
            isinstance(node.body[0], ast.Expr) and
            isinstance(node.body[0].value, (ast.Str, ast.Constant))):
            return True
        return False
```

Skip private methods and dunder methods for docstring requirements.

## Mutable Default Arguments

```python
class MutableDefaultVisitor(ast.NodeVisitor):
    def visit_FunctionDef(self, node):
        for arg in node.args.defaults:
            if isinstance(arg, (ast.List, ast.Dict)):
                self.violations.append((node.lineno,
                    f"Function '{node.name}' has mutable default argument - use None instead"))
            if isinstance(arg, ast.Call) and isinstance(arg.func, ast.Name):
                if arg.func.id in ('set', 'dict', 'list'):
                    self.violations.append((node.lineno,
                        f"Function '{node.name}' has mutable default argument"))
        self.generic_visit(node)
```

## String Formatting

When enforcing f-strings over .format() or %-formatting:

```python
class StringFormatVisitor(ast.NodeVisitor):
    def visit_BinOp(self, node):
        if isinstance(node.op, ast.Mod) and isinstance(node.left, (ast.Str, ast.Constant)):
            self.violations.append((node.lineno, "Use f-strings instead of %-formatting"))
        self.generic_visit(node)

    def visit_Call(self, node):
        if (isinstance(node.func, ast.Attribute) and
            node.func.attr == 'format' and
            isinstance(node.func.value, (ast.Str, ast.Constant))):
            self.violations.append((node.lineno, "Use f-strings instead of .format()"))
        self.generic_visit(node)
```
