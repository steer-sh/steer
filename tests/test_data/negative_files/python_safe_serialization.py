"""
Examples of Python code using safe serialization methods
This should NOT be blocked by the hook
"""

import json
import yaml
import csv
import xml.etree.ElementTree as ET
from dataclasses import dataclass, asdict
from typing import Dict, List, Any

# Good: Using JSON for serialization
def save_user_data_json(user_data, filename):
    """Good: Using JSON to serialize data"""
    with open(filename, 'w') as f:
        json.dump(user_data, f, indent=2)

def load_user_data_json(filename):
    """Good: Using JSON to deserialize data"""
    with open(filename, 'r') as f:
        return json.load(f)

def serialize_object_json(obj):
    """Good: Using JSON dumps"""
    return json.dumps(obj, default=str)  # default=str for datetime objects

def deserialize_object_json(data):
    """Good: Using JSON loads"""
    return json.loads(data)

# Good: Using YAML for configuration
def save_config_yaml(config, filename):
    """Good: Using YAML for configuration files"""
    with open(filename, 'w') as f:
        yaml.dump(config, f, default_flow_style=False)

def load_config_yaml(filename):
    """Good: Loading YAML configuration"""
    with open(filename, 'r') as f:
        return yaml.safe_load(f)  # Use safe_load for security

# Good: Using CSV for tabular data
def save_users_csv(users, filename):
    """Good: Using CSV for tabular data"""
    with open(filename, 'w', newline='') as f:
        writer = csv.DictWriter(f, fieldnames=['id', 'name', 'email'])
        writer.writeheader()
        writer.writerows(users)

def load_users_csv(filename):
    """Good: Loading CSV data"""
    users = []
    with open(filename, 'r') as f:
        reader = csv.DictReader(f)
        for row in reader:
            users.append(row)
    return users

# Good: Using XML for structured data
def save_data_xml(data, filename):
    """Good: Using XML for structured data"""
    root = ET.Element("data")
    for key, value in data.items():
        elem = ET.SubElement(root, key)
        elem.text = str(value)
    
    tree = ET.ElementTree(root)
    tree.write(filename, encoding='utf-8', xml_declaration=True)

def load_data_xml(filename):
    """Good: Loading XML data"""
    tree = ET.parse(filename)
    root = tree.getroot()
    
    data = {}
    for child in root:
        data[child.tag] = child.text
    return data

# Good: Using dataclasses with JSON
@dataclass
class User:
    id: int
    name: str
    email: str
    preferences: Dict[str, Any]

def save_user_dataclass(user: User, filename):
    """Good: Using dataclass with JSON serialization"""
    with open(filename, 'w') as f:
        json.dump(asdict(user), f, indent=2)

def load_user_dataclass(filename) -> User:
    """Good: Loading dataclass from JSON"""
    with open(filename, 'r') as f:
        data = json.load(f)
        return User(**data)

class DataManager:
    """Good: Class using safe serialization methods"""
    
    def __init__(self):
        self.data = {}
    
    def save_to_json_file(self, filename):
        """Good: JSON usage in class method"""
        with open(filename, 'w') as f:
            json.dump(self.data, f, indent=2)
    
    def load_from_json_file(self, filename):
        """Good: JSON loading in class method"""
        with open(filename, 'r') as f:
            self.data = json.load(f)
    
    def serialize_for_network(self, obj):
        """Good: JSON for network serialization"""
        return json.dumps(obj).encode('utf-8')
    
    def deserialize_from_network(self, data_bytes):
        """Good: JSON for network deserialization"""
        return json.loads(data_bytes.decode('utf-8'))

# Good: Safe caching with JSON
class JSONCache:
    """Good: Cache implementation using JSON"""
    
    def __init__(self, cache_dir):
        self.cache_dir = cache_dir
        import os
        os.makedirs(cache_dir, exist_ok=True)
    
    def cache_object(self, key, obj):
        """Good: Caching with JSON"""
        import os
        cache_file = os.path.join(self.cache_dir, f"{key}.json")
        with open(cache_file, 'w') as f:
            json.dump(obj, f, default=str)
    
    def get_cached_object(self, key):
        """Good: Loading cached JSON"""
        import os
        cache_file = os.path.join(self.cache_dir, f"{key}.json")
        try:
            with open(cache_file, 'r') as f:
                return json.load(f)
        except FileNotFoundError:
            return None

# Good: Using msgpack for binary serialization (safer than pickle)
try:
    import msgpack
    
    def serialize_with_msgpack(obj):
        """Good: Using msgpack for binary serialization"""
        return msgpack.packb(obj)
    
    def deserialize_with_msgpack(data):
        """Good: Using msgpack for deserialization"""
        return msgpack.unpackb(data, raw=False)
        
except ImportError:
    # Fallback to JSON if msgpack not available
    def serialize_with_msgpack(obj):
        return json.dumps(obj).encode('utf-8')
    
    def deserialize_with_msgpack(data):
        return json.loads(data.decode('utf-8'))

# Good: Custom serialization for specific objects
class CustomSerializable:
    """Good: Class with custom safe serialization"""
    
    def __init__(self, name, value):
        self.name = name
        self.value = value
    
    def to_dict(self):
        """Good: Convert to dictionary for JSON serialization"""
        return {
            'name': self.name,
            'value': self.value,
            'class_type': self.__class__.__name__
        }
    
    @classmethod
    def from_dict(cls, data):
        """Good: Create instance from dictionary"""
        return cls(data['name'], data['value'])
    
    def to_json(self):
        """Good: Serialize to JSON string"""
        return json.dumps(self.to_dict())
    
    @classmethod
    def from_json(cls, json_str):
        """Good: Deserialize from JSON string"""
        data = json.loads(json_str)
        return cls.from_dict(data)

# Good: Using SQLite for persistent storage (better than shelve)
import sqlite3

class SafeDataStorage:
    """Good: Using SQLite instead of shelve"""
    
    def __init__(self, db_path):
        self.db_path = db_path
        self._init_db()
    
    def _init_db(self):
        """Good: Initialize SQLite database"""
        with sqlite3.connect(self.db_path) as conn:
            conn.execute('''
                CREATE TABLE IF NOT EXISTS data (
                    key TEXT PRIMARY KEY,
                    value TEXT
                )
            ''')
    
    def store(self, key, value):
        """Good: Store data as JSON in SQLite"""
        json_value = json.dumps(value, default=str)
        with sqlite3.connect(self.db_path) as conn:
            conn.execute(
                'INSERT OR REPLACE INTO data (key, value) VALUES (?, ?)',
                (key, json_value)
            )
    
    def retrieve(self, key):
        """Good: Retrieve and deserialize JSON data"""
        with sqlite3.connect(self.db_path) as conn:
            cursor = conn.execute('SELECT value FROM data WHERE key = ?', (key,))
            row = cursor.fetchone()
            if row:
                return json.loads(row[0])
        return None

# Good: Protocol Buffers example (if available)
try:
    import google.protobuf
    
    def serialize_with_protobuf(proto_obj):
        """Good: Using Protocol Buffers for serialization"""
        return proto_obj.SerializeToString()
    
    def deserialize_with_protobuf(data, proto_class):
        """Good: Using Protocol Buffers for deserialization"""
        obj = proto_class()
        obj.ParseFromString(data)
        return obj
        
except ImportError:
    # Protocol Buffers not available
    pass

# Good: Configuration management with safe formats
def load_configuration():
    """Good: Loading configuration from safe formats"""
    config = {}
    
    # Try YAML first
    try:
        with open('config.yaml', 'r') as f:
            config.update(yaml.safe_load(f))
    except FileNotFoundError:
        pass
    
    # Try JSON as fallback
    try:
        with open('config.json', 'r') as f:
            config.update(json.load(f))
    except FileNotFoundError:
        pass
    
    return config

# Good: Safe data exchange formats
def export_data_for_api(data):
    """Good: Export data in safe formats for API"""
    return {
        'json': json.dumps(data, default=str),
        'csv': convert_to_csv(data),
        'xml': convert_to_xml(data)
    }

def convert_to_csv(data):
    """Helper: Convert data to CSV format"""
    import io
    output = io.StringIO()
    if isinstance(data, list) and data:
        writer = csv.DictWriter(output, fieldnames=data[0].keys())
        writer.writeheader()
        writer.writerows(data)
    return output.getvalue()

def convert_to_xml(data):
    """Helper: Convert data to XML format"""
    root = ET.Element("data")
    if isinstance(data, dict):
        for key, value in data.items():
            elem = ET.SubElement(root, key)
            elem.text = str(value)
    return ET.tostring(root, encoding='unicode')