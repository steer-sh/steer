"""
Examples of Python code using dangerous pickle module
This should be blocked by the hook
"""

# Bad: Direct import of pickle
import pickle
import pickletools
from pickle import load, dump, loads, dumps

# Bad: Importing specific functions from pickle
from pickle import Pickler, Unpickler

# Bad: Import with alias
import pickle as pkl

# Bad: Using cPickle (Python 2 style, but still dangerous)
try:
    import cPickle as pickle_fast
except ImportError:
    import pickle as pickle_fast

def save_user_data(user_data, filename):
    """Bad: Using pickle to serialize data"""
    with open(filename, 'wb') as f:
        pickle.dump(user_data, f)

def load_user_data(filename):
    """Bad: Using pickle to deserialize data"""
    with open(filename, 'rb') as f:
        return pickle.load(f)

def serialize_object(obj):
    """Bad: Using pickle dumps"""
    return pickle.dumps(obj)

def deserialize_object(data):
    """Bad: Using pickle loads"""
    return pickle.loads(data)

class DataManager:
    """Bad: Class using pickle for persistence"""
    
    def __init__(self):
        self.data = {}
    
    def save_to_file(self, filename):
        """Bad: Pickle usage in class method"""
        with open(filename, 'wb') as f:
            pickle.dump(self.data, f, protocol=pickle.HIGHEST_PROTOCOL)
    
    def load_from_file(self, filename):
        """Bad: Pickle loading in class method"""
        with open(filename, 'rb') as f:
            self.data = pickle.load(f)
    
    def serialize_for_network(self, obj):
        """Bad: Pickle for network serialization"""
        return pickle.dumps(obj, protocol=2)

# Bad: Using Pickler class directly
def custom_pickle_save(obj, filename):
    """Bad: Using Pickler class"""
    with open(filename, 'wb') as f:
        pickler = pickle.Pickler(f)
        pickler.dump(obj)

# Bad: Using Unpickler class directly
def custom_pickle_load(filename):
    """Bad: Using Unpickler class"""
    with open(filename, 'rb') as f:
        unpickler = pickle.Unpickler(f)
        return unpickler.load()

# Bad: Pickle with custom protocols
def save_with_protocol(obj, filename, protocol=None):
    """Bad: Pickle with different protocols"""
    with open(filename, 'wb') as f:
        if protocol:
            pickle.dump(obj, f, protocol=protocol)
        else:
            pickle.dump(obj, f)

# Bad: Loading arbitrary pickled data
def load_untrusted_data(data_bytes):
    """Bad: Loading potentially untrusted pickled data"""
    try:
        return pickle.loads(data_bytes)
    except Exception as e:
        print(f"Error loading pickled data: {e}")
        return None

# Bad: Pickle in caching mechanism
class PickleCache:
    """Bad: Using pickle for caching"""
    
    def __init__(self, cache_dir):
        self.cache_dir = cache_dir
    
    def cache_object(self, key, obj):
        """Bad: Caching with pickle"""
        cache_file = f"{self.cache_dir}/{key}.pkl"
        with open(cache_file, 'wb') as f:
            pickle.dump(obj, f)
    
    def get_cached_object(self, key):
        """Bad: Loading cached pickle"""
        cache_file = f"{self.cache_dir}/{key}.pkl"
        try:
            with open(cache_file, 'rb') as f:
                return pickle.load(f)
        except FileNotFoundError:
            return None

# Bad: Pickle in web application
def handle_session_data(session_data):
    """Bad: Using pickle for web session data"""
    # Serialize session for storage
    serialized = pickle.dumps(session_data)
    
    # Store in database or file
    store_session(serialized)
    
    # Later retrieve and deserialize
    retrieved = get_session()
    return pickle.loads(retrieved)

# Bad: Pickle for IPC (Inter-Process Communication)
def send_object_to_process(obj, pipe):
    """Bad: Using pickle for IPC"""
    serialized = pickle.dumps(obj)
    pipe.send(serialized)

def receive_object_from_process(pipe):
    """Bad: Receiving pickled objects"""
    serialized = pipe.recv()
    return pickle.loads(serialized)

# Bad: Pickle with compression
def save_compressed_pickle(obj, filename):
    """Bad: Pickle with compression"""
    import gzip
    with gzip.open(filename, 'wb') as f:
        pickle.dump(obj, f)

def load_compressed_pickle(filename):
    """Bad: Loading compressed pickle"""
    import gzip
    with gzip.open(filename, 'rb') as f:
        return pickle.load(f)

# Bad: Multiple pickle operations
def process_data_pipeline(input_file, output_file):
    """Bad: Multiple pickle operations in pipeline"""
    # Load data
    with open(input_file, 'rb') as f:
        data = pickle.load(f)
    
    # Process data
    processed_data = process_data(data)
    
    # Save processed data
    with open(output_file, 'wb') as f:
        pickle.dump(processed_data, f)

def process_data(data):
    """Helper function for processing"""
    return [item * 2 for item in data]

def store_session(data):
    """Placeholder function"""
    pass

def get_session():
    """Placeholder function"""
    return b"placeholder"