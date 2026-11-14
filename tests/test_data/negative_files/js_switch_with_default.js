// JavaScript switch statement with default case (should be ALLOWED)

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
        default:
            result = 'Unknown action: ' + action;
            break;
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
        default:
            console.log('Unexpected status code:', status);
            break;
    }
}

function calculateGrade(score) {
    let grade;
    
    switch (true) {
        case score >= 90:
            grade = 'A';
            break;
        case score >= 80:
            grade = 'B';
            break;
        case score >= 70:
            grade = 'C';
            break;
        case score >= 60:
            grade = 'D';
            break;
        default:
            grade = 'F';
            break;
    }
    
    return grade;
}

module.exports = { handleUserAction, processApiResponse, calculateGrade };