// Code with safe parsing methods
import yaml from 'js-yaml';

function parseConfigFile(content, format) {
  switch (format.toLowerCase()) {
    case 'json':
      return JSON.parse(content);
    
    case 'yaml':
    case 'yml':
      return yaml.load(content);
    
    default:
      throw new Error('Unsupported format');
  }
}

function validateAndParse(input) {
  // Input validation
  if (typeof input !== 'string') {
    throw new TypeError('Input must be a string');
  }
  
  // Safe parsing with validation
  const trimmed = input.trim();
  if (trimmed.startsWith('{') && trimmed.endsWith('}')) {
    return JSON.parse(trimmed);
  }
  
  throw new Error('Invalid format');
}

// Using built-in parsing functions
const parseNumber = (str) => Number.parseFloat(str);
const parseInteger = (str) => Number.parseInt(str, 10);

export { parseConfigFile, validateAndParse, parseNumber, parseInteger };