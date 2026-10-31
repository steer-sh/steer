"""
Examples of Python classes with bad naming conventions
This should be blocked by the hook
"""

# Bad: class names not in PascalCase
class user_manager:  # Bad: should be UserManager
    def __init__(self):
        self.users = []
    
    def add_user(self, user):
        self.users.append(user)

class dataProcessor:  # Bad: should be DataProcessor (first letter lowercase)
    def process_data(self, data):
        return data.upper()

class HTTP_client:  # Bad: should be HttpClient (underscores not allowed)
    def get(self, url):
        return f"GET {url}"

class database_CONNECTION:  # Bad: mixed case with underscores
    def connect(self):
        pass

# Bad: acronyms should follow PascalCase too
class XMLparser:  # Bad: should be XmlParser
    def parse(self, xml_string):
        return {}

class apiCLIENT:  # Bad: should be ApiClient
    def call_api(self):
        pass

# Bad: multiple word classes with poor naming
class file_system_manager:  # Bad: should be FileSystemManager
    def list_files(self):
        return []

class user_authentication_service:  # Bad: should be UserAuthenticationService
    def authenticate(self, username, password):
        return True

# Bad: single letter or very short names
class a:  # Bad: too short, not descriptive
    pass

class db:  # Bad: abbreviation, should be Database
    def query(self, sql):
        return []

# Edge cases
class _private_class:  # Bad: leading underscore but not PascalCase
    pass

class myClass123:  # Bad: should be MyClass123
    pass