"""
Example of dangerous mutable default argument pattern - dict
This should be blocked by the hook
"""

def create_user(name, permissions={}):
    """Bad: mutable dict as default argument"""
    permissions['read'] = True
    return {'name': name, 'permissions': permissions}

def log_event(event, context={}):
    """Bad: mutable dict as default argument"""
    context['timestamp'] = '2024-01-01'
    return f"Event: {event}, Context: {context}"

def merge_settings(new_settings, settings={}):
    """Bad: mutable dict as default argument"""
    settings.update(new_settings)
    return settings

# Also test with sets and other mutable types
def track_items(item, seen=set()):
    """Bad: mutable set as default argument"""
    seen.add(item)
    return len(seen)

def collect_data(data, collection=[]):
    """Bad: mutable list as default argument"""
    collection.extend(data)
    return collection