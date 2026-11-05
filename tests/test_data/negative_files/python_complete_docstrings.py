# Python code with comprehensive docstrings for all functions and classes (should be ALLOWED)

def calculate_total(items, tax_rate):
    """
    Calculate the total cost including tax.
    
    Args:
        items (list): List of items with 'price' keys
        tax_rate (float): Tax rate as decimal (e.g., 0.1 for 10%)
        
    Returns:
        float: Total cost including tax
    """
    total = sum(item['price'] for item in items)
    return total * (1 + tax_rate)

class User:
    """Represents a user with name and email."""
    
    def __init__(self, name, email):
        """
        Initialize a new user.
        
        Args:
            name (str): User's full name
            email (str): User's email address
        """
        self.name = name
        self.email = email
        self.created_at = None
    
    def update_email(self, new_email):
        """
        Update the user's email address.
        
        Args:
            new_email (str): New email address
            
        Returns:
            bool: True if email was updated, False if invalid
        """
        if '@' in new_email:
            self.email = new_email
            return True
        return False
    
    def get_display_name(self):
        """
        Get formatted display name with email.
        
        Returns:
            str: Formatted display name
        """
        return f"{self.name} <{self.email}>"

def process_data(data_list):
    """
    Process a list of data items, extracting valid values.
    
    Args:
        data_list (list): List of dictionaries with 'valid' and 'value' keys
        
    Returns:
        list: List of values from valid items
    """
    results = []
    for item in data_list:
        if item.get('valid'):
            results.append(item['value'])
    return results

class DatabaseManager:
    """Manages database connections and query execution."""
    
    def __init__(self, connection_string):
        """
        Initialize database manager.
        
        Args:
            connection_string (str): Database connection string
        """
        self.connection_string = connection_string
        self.connected = False
    
    def connect(self):
        """
        Establish database connection.
        
        Returns:
            bool: True if connection successful, False otherwise
        """
        try:
            # Simulate connection
            self.connected = True
            return True
        except Exception:
            return False
    
    def execute_query(self, query, params=None):
        """
        Execute a database query.
        
        Args:
            query (str): SQL query to execute
            params (list, optional): Query parameters
            
        Returns:
            dict: Query results with 'rows' and 'affected' keys
            
        Raises:
            RuntimeError: If not connected to database
        """
        if not self.connected:
            raise RuntimeError("Not connected to database")
        
        # Simulate query execution
        return {"rows": [], "affected": 0}

def validate_input(value, min_length=1, max_length=100):
    """
    Validate string input length.
    
    Args:
        value: Input value to validate
        min_length (int): Minimum allowed length
        max_length (int): Maximum allowed length
        
    Returns:
        bool: True if valid, False otherwise
    """
    if not isinstance(value, str):
        return False
    
    return min_length <= len(value) <= max_length

def helper_function(x, y):
    """
    Helper function for arithmetic operations.
    
    Args:
        x (int): First number
        y (int): Second number
        
    Returns:
        int: Sum if x is positive, otherwise returns y
    """
    return x + y if x > 0 else y