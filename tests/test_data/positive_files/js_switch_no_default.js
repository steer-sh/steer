// JavaScript switch statement without default case (should be BLOCKED)

function handleUserAction(action) {
    let result;
    
    switch (action) {
        case 'save':
            result = 'File saved successfully';
            break;
        case 'delete':
            result = 'File deleted successfully';
            break;
        case 'edit':
            result = 'File opened for editing';
            break;
        // Missing default case - this should trigger the hook
    }
    
    return result;
}

function processApiResponse(status) {
    switch (status) {
        case 200:
            console.log('Success');
            break;
        case 404:
            console.log('Not found');
            break;
        case 500:
            console.log('Server error');
            break;
        // No default case here either
    }
}

module.exports = { handleUserAction, processApiResponse };