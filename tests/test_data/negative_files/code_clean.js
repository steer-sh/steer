// Clean JavaScript file without console.log statements
const logger = require('./logger');

function calculateSum(a, b) {
    logger.info(`Calculating sum of ${a} and ${b}`);
    const result = a + b;
    logger.info(`Result: ${result}`);
    return result;
}

function processData(data) {
    logger.info('Processing data:', data);
    
    if (!data) {
        logger.warn('No data provided');
        return null;
    }
    
    const processed = data.map(item => {
        logger.debug('Processing item:', item);
        return item * 2;
    });
    
    logger.info('Processed data:', processed);
    return processed;
}

// Clean application startup
logger.info('Application starting...');

module.exports = { calculateSum, processData };