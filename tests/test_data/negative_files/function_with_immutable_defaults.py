"""
Example of safe immutable default arguments
This should NOT be blocked by the hook
"""

def calculate_total(price, tax_rate=0.08):
    """Good: Immutable float default"""
    return price * (1 + tax_rate)

def format_message(text, prefix="INFO"):
    """Good: Immutable string default"""
    return f"{prefix}: {text}"

def repeat_action(action, count=1):
    """Good: Immutable int default"""
    for i in range(count):
        action()

def is_valid(value, strict=True):
    """Good: Immutable bool default"""
    if strict:
        return value is not None and value != ""
    return value is not None

def process_with_tuple(data, default_coords=(0, 0)):
    """Good: Immutable tuple default"""
    x, y = default_coords
    return data + x + y

def frozen_set_example(items, allowed=frozenset(['read', 'write'])):
    """Good: Immutable frozenset default"""
    return [item for item in items if item in allowed]

# Functions with no defaults at all
def simple_function(a, b):
    """Good: No default arguments"""
    return a + b

def multiple_args(name, age, city):
    """Good: Multiple args, no defaults"""
    return f"{name}, {age}, {city}"