"""
Example of proper default argument patterns using None
This should NOT be blocked by the hook
"""

def add_item(item, items=None):
    """Good: Use None and initialize inside function"""
    if items is None:
        items = []
    items.append(item)
    return items

def process_data(data, cache=None):
    """Good: Use None and initialize inside function"""
    if cache is None:
        cache = {}
    if data in cache:
        return cache[data]
    result = data * 2
    cache[data] = result
    return result

class DataProcessor:
    def __init__(self, config=None):
        """Good: Use None and initialize inside constructor"""
        if config is None:
            config = {}
        self.config = config
    
    def add_values(self, value, values=None):
        """Good: Use None and initialize inside method"""
        if values is None:
            values = []
        values.append(value)
        return values

def create_user(name, permissions=None):
    """Good: Use None and initialize inside function"""
    if permissions is None:
        permissions = {}
    permissions['read'] = True
    return {'name': name, 'permissions': permissions}

def track_items(item, seen=None):
    """Good: Use None and initialize inside function"""
    if seen is None:
        seen = set()
    seen.add(item)
    return len(seen)