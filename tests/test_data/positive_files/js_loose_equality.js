// JavaScript code using loose equality operators (== and !=) (should be BLOCKED)

function validateUser(user) {
    // Using loose equality - should trigger hook
    if (user.id == null) {
        return false;
    }
    
    // Another loose equality check
    if (user.age == 0) {
        console.log('User age is zero');
    }
    
    // Loose inequality
    if (user.status != 'active') {
        return false;
    }
    
    return true;
}

function processFormData(formData) {
    const username = formData.username;
    const email = formData.email;
    
    // Multiple loose equality checks
    if (username == '' || email == '') {
        throw new Error('Username and email are required');
    }
    
    // Loose equality with number comparison
    if (formData.terms == 1) {
        console.log('Terms accepted');
    }
    
    // Another loose inequality
    if (formData.newsletter != 'yes') {
        console.log('Newsletter not subscribed');
    }
    
    return {
        username: username,
        email: email,
        termsAccepted: formData.terms == 1
    };
}

class UserManager {
    constructor() {
        this.users = [];
    }
    
    findUser(id) {
        // Loose equality in array find
        return this.users.find(user => user.id == id);
    }
    
    isActiveUser(user) {
        // Mixed loose equality checks
        return user.status == 'active' && user.deletedAt == null;
    }
    
    updateUserStatus(userId, newStatus) {
        const user = this.findUser(userId);
        
        if (user != null && user.status != newStatus) {
            user.status = newStatus;
            return true;
        }
        
        return false;
    }
}

// Function with multiple loose equality patterns
function compareValues(a, b, c) {
    if (a == b) {
        console.log('a equals b');
    }
    
    if (b != c) {
        console.log('b does not equal c');
    }
    
    // Loose equality with different types
    if (a == '0' || b == false) {
        console.log('Type coercion happening');
    }
    
    return a == b == c; // This would be problematic anyway
}

module.exports = { validateUser, processFormData, UserManager, compareValues };