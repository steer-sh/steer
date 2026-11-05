// JavaScript code using strict equality operators (=== and !==) (should be ALLOWED)

function validateUser(user) {
    // Using strict equality - this should be allowed
    if (user.id === null || user.id === undefined) {
        return false;
    }
    
    // Strict equality check
    if (user.age === 0) {
        console.log('User age is zero');
    }
    
    // Strict inequality
    if (user.status !== 'active') {
        return false;
    }
    
    return true;
}

function processFormData(formData) {
    const username = formData.username;
    const email = formData.email;
    
    // Strict equality checks
    if (username === '' || email === '') {
        throw new Error('Username and email are required');
    }
    
    // Strict equality with number comparison
    if (formData.terms === 1) {
        console.log('Terms accepted');
    }
    
    // Strict inequality
    if (formData.newsletter !== 'yes') {
        console.log('Newsletter not subscribed');
    }
    
    return {
        username: username,
        email: email,
        termsAccepted: formData.terms === 1
    };
}

class UserManager {
    constructor() {
        this.users = [];
    }
    
    findUser(id) {
        // Strict equality in array find
        return this.users.find(user => user.id === id);
    }
    
    isActiveUser(user) {
        // Strict equality checks
        return user.status === 'active' && user.deletedAt === null;
    }
    
    updateUserStatus(userId, newStatus) {
        const user = this.findUser(userId);
        
        if (user !== null && user.status !== newStatus) {
            user.status = newStatus;
            return true;
        }
        
        return false;
    }
}

// Function with strict equality patterns
function compareValues(a, b, c) {
    if (a === b) {
        console.log('a strictly equals b');
    }
    
    if (b !== c) {
        console.log('b does not strictly equal c');
    }
    
    // Proper type checking with strict equality
    if (typeof a === 'string' && a === '0') {
        console.log('a is the string "0"');
    }
    
    if (typeof b === 'boolean' && b === false) {
        console.log('b is the boolean false');
    }
    
    return a === b && b === c;
}

function safeTruthyCheck(value) {
    // Explicit checks instead of truthy/falsy
    if (value === null || value === undefined) {
        return false;
    }
    
    if (typeof value === 'string' && value === '') {
        return false;
    }
    
    if (typeof value === 'number' && value === 0) {
        return false;
    }
    
    if (typeof value === 'boolean' && value === false) {
        return false;
    }
    
    return true;
}

module.exports = { validateUser, processFormData, UserManager, compareValues, safeTruthyCheck };