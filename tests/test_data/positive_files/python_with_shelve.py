"""
Examples of Python code using dangerous shelve module
This should be blocked by the hook
"""

# Bad: Direct import of shelve
import shelve
from shelve import open as shelve_open

# Bad: Import with alias
import shelve as shelf

def store_user_preferences(user_id, preferences):
    """Bad: Using shelve for data storage"""
    with shelve.open('user_prefs.db') as shelf:
        shelf[str(user_id)] = preferences

def get_user_preferences(user_id):
    """Bad: Using shelve for data retrieval"""
    with shelve.open('user_prefs.db', flag='r') as shelf:
        return shelf.get(str(user_id), {})

def update_user_preferences(user_id, new_prefs):
    """Bad: Using shelve for data updates"""
    with shelve.open('user_prefs.db') as shelf:
        if str(user_id) in shelf:
            current_prefs = shelf[str(user_id)]
            current_prefs.update(new_prefs)
            shelf[str(user_id)] = current_prefs

class UserDataManager:
    """Bad: Class using shelve for persistence"""
    
    def __init__(self, db_path='user_data.db'):
        self.db_path = db_path
    
    def save_user(self, user_id, user_data):
        """Bad: Shelve usage in class method"""
        with shelve.open(self.db_path) as db:
            db[str(user_id)] = user_data
    
    def get_user(self, user_id):
        """Bad: Shelve retrieval in class method"""
        with shelve.open(self.db_path, flag='r') as db:
            return db.get(str(user_id))
    
    def delete_user(self, user_id):
        """Bad: Shelve deletion"""
        with shelve.open(self.db_path) as db:
            if str(user_id) in db:
                del db[str(user_id)]
    
    def list_all_users(self):
        """Bad: Iterating over shelve"""
        with shelve.open(self.db_path, flag='r') as db:
            return list(db.keys())

# Bad: Using shelve for caching
class ShelveCache:
    """Bad: Cache implementation using shelve"""
    
    def __init__(self, cache_file='cache.db'):
        self.cache_file = cache_file
    
    def set(self, key, value, ttl=None):
        """Bad: Setting cache values with shelve"""
        with shelve.open(self.cache_file) as cache:
            import time
            cache[key] = {
                'value': value,
                'timestamp': time.time(),
                'ttl': ttl
            }
    
    def get(self, key):
        """Bad: Getting cache values with shelve"""
        with shelve.open(self.cache_file, flag='r') as cache:
            if key in cache:
                item = cache[key]
                if item['ttl'] is None:
                    return item['value']
                
                import time
                if time.time() - item['timestamp'] < item['ttl']:
                    return item['value']
        return None
    
    def clear(self):
        """Bad: Clearing shelve cache"""
        with shelve.open(self.cache_file) as cache:
            cache.clear()

# Bad: Shelve for configuration storage
def save_app_config(config):
    """Bad: Using shelve for configuration"""
    with shelve.open('app_config.db') as config_db:
        config_db['settings'] = config

def load_app_config():
    """Bad: Loading configuration from shelve"""
    with shelve.open('app_config.db', flag='r') as config_db:
        return config_db.get('settings', {})

# Bad: Shelve for session management
class SessionManager:
    """Bad: Session management using shelve"""
    
    def __init__(self, session_db='sessions.db'):
        self.session_db = session_db
    
    def create_session(self, session_id, user_data):
        """Bad: Creating sessions with shelve"""
        with shelve.open(self.session_db) as sessions:
            sessions[session_id] = {
                'user_data': user_data,
                'created_at': self._get_timestamp(),
                'last_accessed': self._get_timestamp()
            }
    
    def get_session(self, session_id):
        """Bad: Retrieving sessions with shelve"""
        with shelve.open(self.session_db) as sessions:
            if session_id in sessions:
                session_data = sessions[session_id]
                session_data['last_accessed'] = self._get_timestamp()
                sessions[session_id] = session_data
                return session_data
        return None
    
    def destroy_session(self, session_id):
        """Bad: Destroying sessions with shelve"""
        with shelve.open(self.session_db) as sessions:
            if session_id in sessions:
                del sessions[session_id]
    
    def _get_timestamp(self):
        import time
        return time.time()

# Bad: Shelve for logging/audit trail
def log_user_action(user_id, action, details):
    """Bad: Using shelve for logging"""
    with shelve.open('audit_log.db') as log_db:
        import time
        log_entry = {
            'user_id': user_id,
            'action': action,
            'details': details,
            'timestamp': time.time()
        }
        
        # Generate a unique key for the log entry
        key = f"{user_id}_{int(time.time() * 1000)}"
        log_db[key] = log_entry

def get_user_actions(user_id):
    """Bad: Retrieving logs from shelve"""
    with shelve.open('audit_log.db', flag='r') as log_db:
        user_actions = []
        for key, entry in log_db.items():
            if entry.get('user_id') == user_id:
                user_actions.append(entry)
        return user_actions

# Bad: Shelve with different flags and protocols
def advanced_shelve_usage():
    """Bad: Advanced shelve usage patterns"""
    
    # Bad: Opening with different flags
    with shelve.open('data.db', flag='c') as db:  # Create if doesn't exist
        db['key1'] = 'value1'
    
    with shelve.open('data.db', flag='w') as db:  # Write access
        db['key2'] = 'value2'
    
    with shelve.open('data.db', flag='r') as db:  # Read-only
        value = db.get('key1')
    
    # Bad: Using shelve with specific protocols
    with shelve.open('data.db', protocol=2) as db:
        db['key3'] = {'nested': 'data'}

# Bad: Shelve for temporary data storage
def process_large_dataset(data):
    """Bad: Using shelve for temporary storage during processing"""
    temp_db_path = 'temp_processing.db'
    
    with shelve.open(temp_db_path) as temp_db:
        # Store intermediate results
        for i, chunk in enumerate(data):
            processed_chunk = process_chunk(chunk)
            temp_db[f'chunk_{i}'] = processed_chunk
        
        # Combine results
        final_result = []
        for key in temp_db.keys():
            final_result.extend(temp_db[key])
    
    # Clean up would happen here
    import os
    try:
        os.remove(temp_db_path)
    except:
        pass
    
    return final_result

def process_chunk(chunk):
    """Helper function for processing chunks"""
    return [item.upper() for item in chunk]