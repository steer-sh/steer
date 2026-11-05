"""
Example of dangerous mutable default argument pattern - list
This should be blocked by the hook
"""

def add_item(item, items=[]):
    """Bad: mutable list as default argument"""
    items.append(item)
    return items

def process_data(data, cache={}):
    """Bad: mutable dict as default argument"""
    if data in cache:
        return cache[data]
    result = data * 2
    cache[data] = result
    return result

class DataProcessor:
    def __init__(self, config={}):
        """Bad: mutable dict in constructor"""
        self.config = config
    
    def add_values(self, value, values=[]):
        """Bad: mutable list in method"""
        values.append(value)
        return values