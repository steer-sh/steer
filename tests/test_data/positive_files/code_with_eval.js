// JavaScript using eval() function
function processUserInput(input) {
  // Dangerous use of eval - security risk
  const result = eval(input);
  return result;
}

function calculateExpression(expr) {
  // Another eval usage
  try {
    return eval(`(${expr})`);
  } catch (e) {
    return null;
  }
}

// Dynamic code execution
function executeCode(code) {
  const func = eval(`(function() { ${code} })`);
  return func();
}

// More eval examples
const dynamicFunction = eval('(x) => x * 2');
const config = eval('({timeout: 5000, retries: 3})');

export { processUserInput, calculateExpression, executeCode };