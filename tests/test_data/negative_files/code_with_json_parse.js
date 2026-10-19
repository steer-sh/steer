// JavaScript using JSON.parse() instead of eval
function processUserInput(input) {
  try {
    // Safe parsing using JSON.parse
    const result = JSON.parse(input);
    return result;
  } catch (e) {
    console.error('Invalid JSON input:', e.message);
    return null;
  }
}

function parseConfiguration(configString) {
  // Safe alternative to eval for configuration
  try {
    return JSON.parse(configString);
  } catch (error) {
    return { error: 'Invalid configuration format' };
  }
}

// Safe data parsing
function parseApiResponse(response) {
  if (typeof response === 'string') {
    return JSON.parse(response);
  }
  return response;
}

const defaultConfig = JSON.parse('{"timeout": 5000, "retries": 3}');

export { processUserInput, parseConfiguration, parseApiResponse };