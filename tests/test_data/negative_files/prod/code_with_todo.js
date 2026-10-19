// JavaScript file with TODO comments in production path (should be BLOCKED)

function calculateRevenue(sales) {
    // TODO: Add validation for sales array
    let revenue = 0;
    
    for (let sale of sales) {
        // FIXME: Handle refunds properly
        revenue += sale.amount;
    }
    
    // XXX: This calculation might be wrong
    return revenue * 0.95; // Apply discount
}

function generateReport(data) {
    // TODO: Implement proper error handling
    console.log('Generating production report');
    
    // HACK: Quick fix for the release
    if (!data.metrics) {
        data.metrics = { revenue: 0, users: 0 };
    }
    
    return {
        revenue: calculateRevenue(data.sales),
        users: data.users.length
    };
}

module.exports = { calculateRevenue, generateReport };