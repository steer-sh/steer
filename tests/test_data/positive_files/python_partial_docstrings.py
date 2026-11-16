# Python code with some functions missing docstrings (should be BLOCKED)

def documented_function(name, age):
    """Return a formatted string with name and age."""
    return f"Name: {name}, Age: {age}"

def undocumented_function(items):
    # Missing docstring - should trigger hook
    return [item.upper() for item in items if isinstance(item, str)]

class PartiallyDocumented:
    """A class with some documented methods."""
    
    def __init__(self, value):
        """Initialize with a value."""
        self.value = value
    
    def documented_method(self):
        """Return the stored value."""
        return self.value
    
    def undocumented_method(self, new_value):
        # Missing docstring - should trigger hook
        old_value = self.value
        self.value = new_value
        return old_value
    
    def another_undocumented_method(self, multiplier):
        # Another missing docstring
        return self.value * multiplier

class UndocumentedClass:
    # Missing class docstring - should trigger hook
    
    def __init__(self, name):
        """Initialize with name."""
        self.name = name
    
    def get_name(self):
        # Missing method docstring
        return self.name

def well_documented_function(data, threshold=0.5):
    """
    Process data based on threshold.
    
    Args:
        data (list): Input data to process
        threshold (float): Threshold value for filtering
        
    Returns:
        list: Filtered and processed data
    """
    return [item for item in data if item > threshold]

def missing_docstring_function(a, b, c=None):
    # No docstring - should trigger hook
    if c is None:
        return a + b
    return a + b + c

class MixedDocumentationClass:
    """A class with mixed documentation."""
    
    def documented_init(self, items):
        """Initialize with items list."""
        self.items = items
    
    def process_items(self):
        # Missing docstring
        return [item * 2 for item in self.items]
    
    def get_count(self):
        """Return the number of items."""
        return len(self.items)
    
    def clear_items(self):
        # Another missing docstring
        self.items = []