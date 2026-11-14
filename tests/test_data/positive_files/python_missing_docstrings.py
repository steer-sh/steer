# Python code with functions and classes missing docstrings (should be BLOCKED)

def calculate_total(items, tax_rate):
    # Missing docstring - should trigger hook
    total = sum(item['price'] for item in items)
    return total * (1 + tax_rate)

class User:
    # Missing class docstring - should trigger hook
    def __init__(self, name, email):
        self.name = name
        self.email = email
        self.created_at = None
    
    def update_email(self, new_email):
        # Missing method docstring - should trigger hook
        if '@' in new_email:
            self.email = new_email
            return True
        return False
    
    def get_display_name(self):
        # Another missing docstring
        return f"{self.name} <{self.email}>"

def process_data(data_list):
    # No docstring here either
    results = []
    for item in data_list:
        if item.get('valid'):
            results.append(item['value'])
    return results

class DatabaseManager:
    # Missing class docstring
    
    def __init__(self, connection_string):
        # Missing __init__ docstring
        self.connection_string = connection_string
        self.connected = False
    
    def connect(self):
        # Missing method docstring
        try:
            # Simulate connection
            self.connected = True
            return True
        except Exception:
            return False
    
    def execute_query(self, query, params=None):
        # Missing docstring for complex method
        if not self.connected:
            raise RuntimeError("Not connected to database")
        
        # Simulate query execution
        return {"rows": [], "affected": 0}

def validate_input(value, min_length=1, max_length=100):
    # Missing docstring with complex parameters
    if not isinstance(value, str):
        return False
    
    return min_length <= len(value) <= max_length

# Global function without docstring
def helper_function(x, y):
    return x + y if x > 0 else y