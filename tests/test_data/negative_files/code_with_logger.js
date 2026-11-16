// JavaScript using proper logging library
import logger from './logger';

function processUser(user) {
  logger.info('Processing user:', user.name);
  
  if (user.id < 0) {
    logger.warn('Invalid user ID detected', { userId: user.id });
    return;
  }
  
  // Using structured logging
  logger.debug('User validation passed', {
    userId: user.id,
    userName: user.name
  });
  
  // Professional logging approach
  logger.info('User processing completed successfully', {
    userId: user.id,
    timestamp: new Date().toISOString()
  });
}

export { processUser };