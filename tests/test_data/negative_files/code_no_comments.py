import json
from typing import List, Dict, Any

def process_data(data: List[Dict[str, Any]]) -> List[Dict[str, Any]]:
    results = []
    
    for item in data:
        if 'id' in item and 'name' in item:
            processed = {
                'id': item['id'],
                'name': item['name'],
                'status': 'processed'
            }
            results.append(processed)
    
    return results

class DataProcessor:
    def __init__(self, config: Dict[str, Any]):
        self.config = config
    
    def run(self):
        return {"status": "completed"}
    
    def validate_data(self, data):
        return isinstance(data, list) and len(data) > 0