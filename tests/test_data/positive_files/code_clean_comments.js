// Code with proper comments only
/**
 * Calculate the total price for items in a cart
 * @param {Array} items - Array of items with price property
 * @returns {number} Total price
 */
function calculateTotal(items) {
  let total = 0;
  
  // Validate input parameters
  if (!Array.isArray(items)) {
    throw new Error('Items must be an array');
  }
  
  // Sum up all item prices
  for (const item of items) {
    if (typeof item.price === 'number' && item.price >= 0) {
      total += item.price;
    }
  }
  
  return total;
}

/**
 * Process an order and calculate totals
 */
function processOrder(order) {
  // Validate order structure
  if (!order || !order.items) {
    return null;
  }
  
  return calculateTotal(order.items);
}