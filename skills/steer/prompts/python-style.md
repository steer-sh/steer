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
    def visit_FunctionDef(self, node):
        # Skip dunder methods and private methods
        if node.name.startswith('__') and node.name.endswith('__'):
            self.generic_visit(node)
            return
        if node.name.startswith('_'):
            self.generic_visit(node)
            return
        if not self._is_snake_case(node.name):
            self.violations.append((node.lineno, f"Function '{node.name}' should be snake_case"))
        self.generic_visit(node)

    def _is_snake_case(self, name):
        return name.islower() and name.replace('_', '').isalnum()

    def _is_pascal_case(self, name):
        return name[0].isupper() and '_' not in name and name.isalnum()
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
