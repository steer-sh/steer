"""
Examples of Python code using safer alternatives to pickle/shelve
This should NOT be blocked by the hook
"""

import json
import sqlite3
import redis
from dataclasses import dataclass, asdict
from typing import Dict, List, Any, Optional
import hashlib
import time

# Good: Using SQLite as a safer alternative to shelve
class SQLiteStorage:
    """Good: SQLite-based storage instead of shelve"""
    
    def __init__(self, db_path='storage.db'):
        self.db_path = db_path
        self._initialize_db()
    
    def _initialize_db(self):
        """Initialize SQLite database with proper schema"""
        with sqlite3.connect(self.db_path) as conn:
            conn.execute('''
                CREATE TABLE IF NOT EXISTS key_value_store (
                    key TEXT PRIMARY KEY,
                    value TEXT,
                    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
                    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
                )
            ''')
    
    def set(self, key: str, value: Any) -> None:
        """Store value as JSON"""
        json_value = json.dumps(value, default=str)
        with sqlite3.connect(self.db_path) as conn:
            conn.execute('''
                INSERT OR REPLACE INTO key_value_store (key, value, updated_at) 
                VALUES (?, ?, CURRENT_TIMESTAMP)
            ''', (key, json_value))
    
    def get(self, key: str) -> Optional[Any]:
        """Retrieve and deserialize value"""
        with sqlite3.connect(self.db_path) as conn:
            cursor = conn.execute('SELECT value FROM key_value_store WHERE key = ?', (key,))
            row = cursor.fetchone()
            if row:
                return json.loads(row[0])
        return None
    
    def delete(self, key: str) -> bool:
        """Delete a key-value pair"""
        with sqlite3.connect(self.db_path) as conn:
            cursor = conn.execute('DELETE FROM key_value_store WHERE key = ?', (key,))
            return cursor.rowcount > 0
    
    def keys(self) -> List[str]:
        """Get all keys"""
        with sqlite3.connect(self.db_path) as conn:
            cursor = conn.execute('SELECT key FROM key_value_store')
            return [row[0] for row in cursor.fetchall()]

# Good: Redis-based caching (safer than pickle for caching)
class RedisCache:
    """Good: Redis-based cache with JSON serialization"""
    
    def __init__(self, host='localhost', port=6379, db=0):
        try:
            self.redis_client = redis.Redis(host=host, port=port, db=db, decode_responses=True)
            self.redis_client.ping()  # Test connection
        except redis.ConnectionError:
            # Fallback to in-memory cache if Redis not available
            self.redis_client = None
            self._memory_cache = {}
    
    def set(self, key: str, value: Any, ttl: int = None) -> None:
        """Set value with optional TTL"""
        json_value = json.dumps(value, default=str)
        
        if self.redis_client:
            if ttl:
                self.redis_client.setex(key, ttl, json_value)
            else:
                self.redis_client.set(key, json_value)
        else:
            # Fallback to memory cache
            self._memory_cache[key] = {
                'value': json_value,
                'expires_at': time.time() + ttl if ttl else None
            }
    
    def get(self, key: str) -> Optional[Any]:
        """Get value from cache"""
        if self.redis_client:
            json_value = self.redis_client.get(key)
            if json_value:
                return json.loads(json_value)
        else:
            # Fallback to memory cache
            if key in self._memory_cache:
                cache_item = self._memory_cache[key]
                if cache_item['expires_at'] is None or time.time() < cache_item['expires_at']:
                    return json.loads(cache_item['value'])
                else:
                    del self._memory_cache[key]
        return None

# Good: File-based storage with JSON
class JSONFileStorage:
    """Good: File-based storage using JSON"""
    
    def __init__(self, base_dir='data'):
        self.base_dir = base_dir
        import os
        os.makedirs(base_dir, exist_ok=True)
    
    def save(self, key: str, data: Any) -> None:
        """Save data to JSON file"""
        import os
        safe_key = self._sanitize_key(key)
        file_path = os.path.join(self.base_dir, f"{safe_key}.json")
        
        with open(file_path, 'w') as f:
            json.dump(data, f, indent=2, default=str)
    
    def load(self, key: str) -> Optional[Any]:
        """Load data from JSON file"""
        import os
        safe_key = self._sanitize_key(key)
        file_path = os.path.join(self.base_dir, f"{safe_key}.json")
        
        try:
            with open(file_path, 'r') as f:
                return json.load(f)
        except FileNotFoundError:
            return None
    
    def _sanitize_key(self, key: str) -> str:
        """Sanitize key for use as filename"""
        return hashlib.md5(key.encode()).hexdigest()

# Good: Using dataclasses for structured data
@dataclass
class UserPreferences:
    theme: str = "light"
    language: str = "en"
    notifications: bool = True
    features: Dict[str, bool] = None
    
    def __post_init__(self):
        if self.features is None:
            self.features = {}
    
    def to_json(self) -> str:
        """Serialize to JSON"""
        return json.dumps(asdict(self))
    
    @classmethod
    def from_json(cls, json_str: str) -> 'UserPreferences':
        """Deserialize from JSON"""
        data = json.loads(json_str)
        return cls(**data)

# Good: Session management with safe serialization
class SessionManager:
    """Good: Session management using safe serialization"""
    
    def __init__(self, storage_backend='sqlite'):
        if storage_backend == 'sqlite':
            self.storage = SQLiteStorage('sessions.db')
        elif storage_backend == 'redis':
            self.storage = RedisCache()
        else:
            self.storage = JSONFileStorage('sessions')
    
    def create_session(self, session_id: str, user_data: Dict) -> None:
        """Create a new session"""
        session_data = {
            'user_data': user_data,
            'created_at': time.time(),
            'last_accessed': time.time()
        }
        self.storage.set(session_id, session_data)
    
    def get_session(self, session_id: str) -> Optional[Dict]:
        """Get session data"""
        session_data = self.storage.get(session_id)
        if session_data:
            session_data['last_accessed'] = time.time()
            self.storage.set(session_id, session_data)
        return session_data
    
    def destroy_session(self, session_id: str) -> None:
        """Destroy a session"""
        if hasattr(self.storage, 'delete'):
            self.storage.delete(session_id)

# Good: Configuration management with multiple safe formats
class ConfigManager:
    """Good: Configuration management with safe serialization"""
    
    def __init__(self, config_file='config.json'):
        self.config_file = config_file
        self.config = self._load_config()
    
    def _load_config(self) -> Dict:
        """Load configuration from file"""
        try:
            with open(self.config_file, 'r') as f:
                return json.load(f)
        except FileNotFoundError:
            return {}
    
    def save_config(self) -> None:
        """Save configuration to file"""
        with open(self.config_file, 'w') as f:
            json.dump(self.config, f, indent=2)
    
    def get(self, key: str, default=None):
        """Get configuration value"""
        return self.config.get(key, default)
    
    def set(self, key: str, value: Any) -> None:
        """Set configuration value"""
        self.config[key] = value
        self.save_config()

# Good: Message queue with JSON serialization
class MessageQueue:
    """Good: Message queue using safe serialization"""
    
    def __init__(self, queue_file='queue.jsonl'):
        self.queue_file = queue_file
    
    def enqueue(self, message: Dict) -> None:
        """Add message to queue"""
        message_with_timestamp = {
            'timestamp': time.time(),
            'data': message
        }
        
        with open(self.queue_file, 'a') as f:
            f.write(json.dumps(message_with_timestamp) + '\n')
    
    def dequeue(self) -> Optional[Dict]:
        """Get next message from queue"""
        try:
            with open(self.queue_file, 'r') as f:
                lines = f.readlines()
            
            if lines:
                # Get first message
                first_line = lines[0].strip()
                message = json.loads(first_line)
                
                # Write remaining messages back
                with open(self.queue_file, 'w') as f:
                    f.writelines(lines[1:])
                
                return message['data']
        except FileNotFoundError:
            pass
        return None

# Good: Data export/import with multiple safe formats
class DataExporter:
    """Good: Data export using safe formats"""
    
    @staticmethod
    def export_to_json(data: Any, filename: str) -> None:
        """Export data to JSON"""
        with open(filename, 'w') as f:
            json.dump(data, f, indent=2, default=str)
    
    @staticmethod
    def import_from_json(filename: str) -> Any:
        """Import data from JSON"""
        with open(filename, 'r') as f:
            return json.load(f)
    
    @staticmethod
    def export_to_csv(data: List[Dict], filename: str) -> None:
        """Export tabular data to CSV"""
        import csv
        if not data:
            return
        
        with open(filename, 'w', newline='') as f:
            writer = csv.DictWriter(f, fieldnames=data[0].keys())
            writer.writeheader()
            writer.writerows(data)
    
    @staticmethod
    def import_from_csv(filename: str) -> List[Dict]:
        """Import data from CSV"""
        import csv
        data = []
        with open(filename, 'r') as f:
            reader = csv.DictReader(f)
            for row in reader:
                data.append(dict(row))
        return data

# Good: Audit logging with safe serialization
class AuditLogger:
    """Good: Audit logging using safe serialization"""
    
    def __init__(self, log_file='audit.jsonl'):
        self.log_file = log_file
    
    def log_action(self, user_id: str, action: str, details: Dict = None) -> None:
        """Log user action"""
        log_entry = {
            'timestamp': time.time(),
            'user_id': user_id,
            'action': action,
            'details': details or {},
            'session_id': self._get_session_id()
        }
        
        with open(self.log_file, 'a') as f:
            f.write(json.dumps(log_entry) + '\n')
    
    def get_user_actions(self, user_id: str) -> List[Dict]:
        """Get all actions for a user"""
        actions = []
        try:
            with open(self.log_file, 'r') as f:
                for line in f:
                    entry = json.loads(line.strip())
                    if entry.get('user_id') == user_id:
                        actions.append(entry)
        except FileNotFoundError:
            pass
        return actions
    
    def _get_session_id(self) -> str:
        """Get current session ID (placeholder)"""
        return "session_123"

# Good: Using modern serialization libraries
try:
    import msgpack
    
    class MsgPackStorage:
        """Good: Using MessagePack for binary serialization"""
        
        def __init__(self, file_path='data.msgpack'):
            self.file_path = file_path
        
        def save(self, data: Any) -> None:
            """Save data using MessagePack"""
            with open(self.file_path, 'wb') as f:
                msgpack.pack(data, f)
        
        def load(self) -> Any:
            """Load data using MessagePack"""
            try:
                with open(self.file_path, 'rb') as f:
                    return msgpack.unpack(f, raw=False)
            except FileNotFoundError:
                return None
                
except ImportError:
    # MessagePack not available, use JSON fallback
    class MsgPackStorage:
        def __init__(self, file_path='data.json'):
            self.file_path = file_path
        
        def save(self, data: Any) -> None:
            with open(self.file_path, 'w') as f:
                json.dump(data, f)
        
        def load(self) -> Any:
            try:
                with open(self.file_path, 'r') as f:
                    return json.load(f)
            except FileNotFoundError:
                return None