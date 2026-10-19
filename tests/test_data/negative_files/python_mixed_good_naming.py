"""
Complex Python code with proper naming conventions throughout
This should NOT be blocked by the hook
"""

from typing import Dict, List, Optional, Union
import json
import logging

# Good: Constants with ALL_CAPS
API_VERSION = "v1"
MAX_RETRY_ATTEMPTS = 3
DEFAULT_PAGE_SIZE = 50

# Good: Module-level variables
logger = logging.getLogger(__name__)

class DatabaseManager:
    """Good: PascalCase class name"""
    
    def __init__(self, connection_string: str):
        self.connection_string = connection_string
        self._connection_pool = None
        self.__secret_key = "secret"  # Good: private attribute
    
    def connect_to_database(self) -> bool:
        """Good: snake_case method name"""
        try:
            # Good: local variables in snake_case
            max_connections = 10
            timeout_seconds = 30
            retry_count = 0
            
            while retry_count < MAX_RETRY_ATTEMPTS:
                connection_successful = self._attempt_connection(
                    max_connections, timeout_seconds
                )
                if connection_successful:
                    return True
                retry_count += 1
            
            return False
        except Exception as error_message:
            logger.error(f"Connection failed: {error_message}")
            return False
    
    def _attempt_connection(self, max_conn: int, timeout: int) -> bool:
        """Good: private method with snake_case"""
        # Simulate connection logic
        return True
    
    def get_user_data(self, user_id: int) -> Optional[Dict]:
        """Good: snake_case method returning proper types"""
        user_query = "SELECT * FROM users WHERE id = ?"
        result_set = self._execute_query(user_query, (user_id,))
        return result_set

    def _execute_query(self, query: str, params: tuple) -> Optional[Dict]:
        """Good: parameterized query helper"""
        return {"id": 1, "name": "John Doe"}

class UserService:
    """Good: PascalCase class for service layer"""
    
    def __init__(self, db_manager: DatabaseManager):
        self.db_manager = db_manager
        self.cache_enabled = True
        self.user_cache = {}
    
    def create_new_user(self, user_data: Dict) -> int:
        """Good: descriptive snake_case method name"""
        # Good: parameter and variable naming
        first_name = user_data.get("first_name")
        last_name = user_data.get("last_name")
        email_address = user_data.get("email")
        phone_number = user_data.get("phone")
        
        # Good: validation with descriptive variables
        is_valid_email = self._validate_email(email_address)
        is_valid_phone = self._validate_phone(phone_number)
        
        if not (is_valid_email and is_valid_phone):
            raise ValueError("Invalid user data provided")
        
        # Good: method call with proper naming
        new_user_id = self._insert_user_record(user_data)
        return new_user_id
    
    def _validate_email(self, email: str) -> bool:
        """Good: private validation method"""
        email_pattern = r'^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$'
        import re
        return bool(re.match(email_pattern, email))
    
    def _validate_phone(self, phone: str) -> bool:
        """Good: private validation method"""
        return len(phone) >= 10
    
    def _insert_user_record(self, data: Dict) -> int:
        """Good: private database operation"""
        return 12345  # Simulated user ID

class ApiController:
    """Good: PascalCase controller class"""
    
    def __init__(self, user_service: UserService):
        self.user_service = user_service
        self.request_count = 0
    
    def handle_user_creation(self, request_data: str) -> Dict:
        """Good: snake_case handler method"""
        try:
            # Good: JSON parsing with proper variable names
            parsed_data = json.loads(request_data)
            validation_result = self._validate_request_data(parsed_data)
            
            if not validation_result["is_valid"]:
                return {
                    "status": "error",
                    "message": validation_result["error_message"]
                }
            
            # Good: service call with proper naming
            new_user_id = self.user_service.create_new_user(parsed_data)
            
            # Good: response building
            success_response = {
                "status": "success",
                "user_id": new_user_id,
                "message": "User created successfully"
            }
            return success_response
            
        except Exception as processing_error:
            error_response = {
                "status": "error", 
                "message": f"Processing failed: {processing_error}"
            }
            return error_response
    
    def _validate_request_data(self, data: Dict) -> Dict:
        """Good: private validation with descriptive return"""
        required_fields = ["first_name", "last_name", "email"]
        missing_fields = []
        
        for field_name in required_fields:
            if field_name not in data:
                missing_fields.append(field_name)
        
        if missing_fields:
            return {
                "is_valid": False,
                "error_message": f"Missing fields: {', '.join(missing_fields)}"
            }
        
        return {"is_valid": True, "error_message": None}

# Good: Utility functions with snake_case
def calculate_pagination_offset(page_number: int, page_size: int = DEFAULT_PAGE_SIZE) -> int:
    """Good: utility function with proper naming"""
    if page_number < 1:
        page_number = 1
    return (page_number - 1) * page_size

def format_user_display_name(first_name: str, last_name: str) -> str:
    """Good: formatting function with descriptive name"""
    return f"{first_name.strip()} {last_name.strip()}"

def get_environment_config() -> Dict[str, str]:
    """Good: configuration getter function"""
    import os
    
    config_values = {
        "database_url": os.getenv("DATABASE_URL", "sqlite:///default.db"),
        "api_key": os.getenv("API_KEY", ""),
        "debug_mode": os.getenv("DEBUG", "false").lower()
    }
    return config_values

# Good: Main execution block
if __name__ == "__main__":
    # Good: main execution variables
    config_data = get_environment_config()
    db_connection_string = config_data["database_url"]
    
    # Good: object instantiation with proper naming
    database_manager = DatabaseManager(db_connection_string)
    user_service = UserService(database_manager)
    api_controller = ApiController(user_service)
    
    # Good: sample usage
    sample_request = json.dumps({
        "first_name": "Jane",
        "last_name": "Doe", 
        "email": "jane.doe@example.com",
        "phone": "555-123-4567"
    })
    
    result = api_controller.handle_user_creation(sample_request)
    print(f"Result: {result}")