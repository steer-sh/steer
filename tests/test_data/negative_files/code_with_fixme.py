# Python file with FIXME comments in non-production path (should be ALLOWED)

def process_data(data_list):
    # TODO: Add proper input validation
    results = []
    
    for item in data_list:
        # FIXME: This doesn't handle None values
        if item > 0:
            results.append(item * 2)
    
    # XXX: Temporary workaround
    return results if results else [0]

class DataProcessor:
    def __init__(self):
        # TODO: Initialize with configuration
        self.processed_count = 0
    
    def transform(self, value):
        # HACK: Quick fix for negative values
        if value < 0:
            value = 0
        
        # FIXME: Add proper error handling
        self.processed_count += 1
        return value * 1.5