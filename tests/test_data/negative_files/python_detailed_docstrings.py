# Python code with detailed docstrings following best practices (should be ALLOWED)

from typing import List, Dict, Optional, Union
from datetime import datetime

def calculate_statistics(data: List[float]) -> Dict[str, float]:
    """
    Calculate comprehensive statistics for a dataset.
    
    This function computes mean, median, standard deviation, and range
    for a given list of numerical values.
    
    Args:
        data (List[float]): A list of numerical values. Must not be empty.
        
    Returns:
        Dict[str, float]: A dictionary containing:
            - 'mean': Arithmetic mean of the values
            - 'median': Middle value when sorted
            - 'std_dev': Standard deviation
            - 'range': Difference between max and min values
            
    Raises:
        ValueError: If the data list is empty
        TypeError: If data contains non-numeric values
        
    Example:
        >>> calculate_statistics([1, 2, 3, 4, 5])
        {'mean': 3.0, 'median': 3.0, 'std_dev': 1.58, 'range': 4.0}
    """
    if not data:
        raise ValueError("Data list cannot be empty")
    
    import statistics
    
    return {
        'mean': statistics.mean(data),
        'median': statistics.median(data),
        'std_dev': statistics.stdev(data) if len(data) > 1 else 0.0,
        'range': max(data) - min(data)
    }

class DataProcessor:
    """
    A comprehensive data processing utility class.
    
    This class provides methods for cleaning, transforming, and analyzing
    various types of data. It maintains internal state for processing
    configurations and cached results.
    
    Attributes:
        config (Dict[str, any]): Processing configuration options
        cache_enabled (bool): Whether to cache processing results
        _cache (Dict[str, any]): Internal cache storage
        
    Example:
        >>> processor = DataProcessor({'normalize': True})
        >>> result = processor.process_dataset(raw_data)
    """
    
    def __init__(self, config: Optional[Dict[str, any]] = None):
        """
        Initialize the data processor with configuration.
        
        Args:
            config (Optional[Dict[str, any]]): Configuration dictionary.
                Supported keys:
                - 'normalize' (bool): Whether to normalize data
                - 'remove_outliers' (bool): Whether to remove outliers
                - 'fill_missing' (str): Strategy for missing values ('mean', 'median', 'zero')
                Defaults to empty configuration if None.
        """
        self.config = config or {}
        self.cache_enabled = self.config.get('cache', True)
        self._cache = {}
    
    def clean_data(self, data: List[Dict[str, any]]) -> List[Dict[str, any]]:
        """
        Clean raw data by removing invalid entries and standardizing formats.
        
        This method performs several cleaning operations:
        1. Removes entries with missing required fields
        2. Standardizes data types
        3. Removes duplicate entries
        4. Validates data ranges
        
        Args:
            data (List[Dict[str, any]]): Raw data as list of dictionaries
            
        Returns:
            List[Dict[str, any]]: Cleaned data with same structure
            
        Raises:
            ValueError: If data format is invalid
            
        Note:
            Required fields are determined by the first valid entry.
            Numeric fields are converted to float when possible.
        """
        if not data:
            return []
        
        # Implementation would go here
        return data
    
    def transform_data(self, data: List[Dict[str, any]], 
                      transformations: List[str]) -> List[Dict[str, any]]:
        """
        Apply a series of transformations to the data.
        
        Available transformations:
        - 'normalize': Scale numeric values to 0-1 range
        - 'log_transform': Apply logarithmic transformation
        - 'standardize': Convert to z-scores (mean=0, std=1)
        - 'categorical_encode': Convert categories to numeric codes
        
        Args:
            data (List[Dict[str, any]]): Input data to transform
            transformations (List[str]): List of transformation names to apply
            
        Returns:
            List[Dict[str, any]]: Transformed data
            
        Raises:
            ValueError: If unknown transformation is specified
            RuntimeError: If transformation fails due to data issues
            
        Warning:
            Some transformations may permanently alter the data structure.
            Consider making a copy if you need to preserve the original.
        """
        # Implementation would go here
        return data
    
    def analyze_patterns(self, data: List[Dict[str, any]], 
                        pattern_type: str = 'correlation') -> Dict[str, any]:
        """
        Analyze patterns in the processed data.
        
        Supports multiple types of pattern analysis:
        - 'correlation': Compute correlation matrices
        - 'clustering': Identify data clusters
        - 'anomaly': Detect outliers and anomalies
        - 'trend': Identify temporal trends
        
        Args:
            data (List[Dict[str, any]]): Data to analyze
            pattern_type (str): Type of pattern analysis to perform.
                Defaults to 'correlation'.
                
        Returns:
            Dict[str, any]: Analysis results. Structure depends on pattern_type:
                - correlation: {'matrix': np.array, 'significant': List[tuple]}
                - clustering: {'clusters': List[List[int]], 'centers': List[List[float]]}
                - anomaly: {'outliers': List[int], 'scores': List[float]}
                - trend: {'slope': float, 'r_squared': float, 'p_value': float}
                
        Raises:
            ValueError: If pattern_type is not supported
            RuntimeError: If analysis fails due to insufficient data
            
        See Also:
            clean_data: For data preprocessing
            transform_data: For data transformation options
        """
        # Implementation would go here
        return {}

def create_report(analysis_results: Dict[str, any], 
                 output_format: str = 'json') -> Union[str, Dict[str, any]]:
    """
    Generate a comprehensive analysis report.
    
    Creates a formatted report from analysis results, supporting multiple
    output formats for different use cases.
    
    Args:
        analysis_results (Dict[str, any]): Results from data analysis
        output_format (str): Output format ('json', 'html', 'markdown').
            Defaults to 'json'.
            
    Returns:
        Union[str, Dict[str, any]]: Formatted report. Type depends on format:
            - 'json': Returns dict suitable for JSON serialization
            - 'html': Returns HTML string with embedded CSS
            - 'markdown': Returns Markdown-formatted string
            
    Raises:
        ValueError: If output_format is not supported
        KeyError: If analysis_results is missing required keys
        
    Example:
        >>> results = {'mean': 5.0, 'std_dev': 1.2}
        >>> report = create_report(results, 'markdown')
        >>> print(report)
        # Data Analysis Report
        - Mean: 5.0
        - Standard Deviation: 1.2
        
    Note:
        HTML output includes inline CSS for standalone viewing.
        Markdown output follows GitHub-flavored markdown syntax.
    """
    # Implementation would go here
    return analysis_results