// JavaScript file with console.log statements
function calculateSum(a, b) {
    console.log(`Calculating sum of ${a} and ${b}`);
    const result = a + b;
    console.log(`Result: ${result}`);
    return result;
}

function processData(data) {
    console.log('Processing data:', data);
    
    if (!data) {
        console.log('No data provided');
        return null;
    }
    
    const processed = data.map(item => {
        console.log('Processing item:', item);
        return item * 2;
    });
    
    console.log('Processed data:', processed);
    return processed;
}

// This should trigger the console.log hook
console.log('Application starting...');

module.exports = { calculateSum, processData };