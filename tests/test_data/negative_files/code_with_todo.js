// JavaScript file with TODO comments in non-production path (should be ALLOWED)

function calculateTotal(items) {
    // TODO: Add validation for items array
    let total = 0;
    
    for (let item of items) {
        // FIXME: Handle negative prices properly
        total += item.price;
    }
    
    // XXX: This might overflow for large numbers
    return total;
}

function processOrder(order) {
    // TODO: Implement proper error handling
    console.log('Processing order:', order.id);
    
    // HACK: Quick fix for the demo
    if (!order.items) {
        order.items = [];
    }
    
    return calculateTotal(order.items);
}

module.exports = { calculateTotal, processOrder };