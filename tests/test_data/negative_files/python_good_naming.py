"""
Examples of Python code with proper naming conventions
This should NOT be blocked by the hook
"""

# Good: class names in PascalCase
class UserManager:
    def __init__(self):
        self.users = []
    
    def add_user(self, user):
        self.users.append(user)

class DataProcessor:
    def process_data(self, data):
        return data.upper()

class HttpClient:
    def get(self, url):
        return f"GET {url}"

class DatabaseConnection:
    def connect(self):
        pass

# Good: proper acronym handling in PascalCase
class XmlParser:
    def parse(self, xml_string):
        return {}

class ApiClient:
    def call_api(self):
        pass

# Good: multi-word classes
class FileSystemManager:
    def list_files(self):
        return []

class UserAuthenticationService:
    def authenticate(self, username, password):
        return True

# Good: function names in snake_case
def calculate_total(items):
    return sum(items)

def process_data(data):
    return data.strip()

def get_user_by_id(user_id):
    return f"User {user_id}"

def http_request(url):
    return f"Requesting {url}"

# Good: module-level variables in snake_case (non-literal RHS avoids constant heuristic)
user_name = str("john")
max_retries = int(3)
is_valid_email = bool(True)
database_connection = DatabaseConnection()

def good_naming_example():
    # Good: local variables with proper naming
    first_name = "Jane"
    last_name = "Doe"
    phone_number = "555-1234"
    email_address = "jane@example.com"
    
    return first_name, last_name

# Good: method names in classes
class UserService:
    def create_user(self, data):
        pass
    
    def delete_user(self, user_id):
        pass
    
    def update_user_profile(self, user_id, profile):
        pass

# Good: constants in ALL_CAPS
MAX_CONNECTIONS = 100
DEFAULT_TIMEOUT = 30
API_BASE_URL = "https://api.example.com"

# Good: function parameters with proper naming
def process_order(order_data, user_info, payment_details):
    return True

def send_email(to_address, from_address, subject_line):
    pass

# Good: loop variables and comprehensions
def good_loops():
    items = [1, 2, 3, 4, 5]
    
    # Good: loop variable naming
    for item_value in items:
        print(item_value)
    
    # Good: comprehension variable naming
    result = [item_value * 2 for item_value in items]
    return result

# Good: exception handling with proper naming
def handle_errors():
    try:
        risky_operation()
    except Exception as error_message:
        print(error_message)

# Good: private methods and attributes
class SecureService:
    def __init__(self):
        self._private_data = {}
        self.__secret_key = "secret"
    
    def _helper_method(self):
        pass
    
    def __private_method(self):
        pass

# Good: built-in overrides (allowed naming patterns)
class CustomList:
    def __init__(self):
        self.items = []
    
    def __len__(self):  # Good: dunder methods
        return len(self.items)
    
    def __str__(self):  # Good: dunder methods
        return str(self.items)

def risky_operation():
    """Helper function for exception example"""
    pass