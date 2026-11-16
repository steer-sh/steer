// TypeScript code using strict equality operators (should be ALLOWED)

interface User {
    id: number;
    name: string;
    email: string;
    age?: number;
    status: 'active' | 'inactive' | 'pending';
    preferences?: Record<string, any>;
}

function validateUserData(user: User): boolean {
    // Using strict equality - this should be allowed
    if (user.id === null || user.id === undefined) {
        return false;
    }
    
    // Strict equality with optional property
    if (user.age === undefined) {
        console.log('Age not provided');
    }
    
    // Strict inequality
    if (user.status !== 'active') {
        return false;
    }
    
    return true;
}

class ApiClient {
    private baseUrl: string;
    
    constructor(baseUrl: string) {
        this.baseUrl = baseUrl;
    }
    
    async fetchUser(id: number): Promise<User | null> {
        const response = await fetch(`${this.baseUrl}/users/${id}`);
        
        // Strict equality with response status
        if (response.status === 200) {
            return response.json();
        }
        
        // Strict inequality
        if (response.status !== 404) {
            console.error('Unexpected error');
        }
        
        return null;
    }
    
    validateResponse(data: any): boolean {
        // Strict equality checks
        if (data.success === true && data.error === null) {
            return true;
        }
        
        return false;
    }
}

function processApiData(data: any[]): User[] {
    return data.filter((item: any) => {
        // Strict equality in filter
        return item.type === 'user' && item.active !== false;
    }).map((item: any) => ({
        id: item.id,
        name: item.name,
        email: item.email,
        status: item.status,
        age: item.age
    }));
}

// Generic function with strict equality
function compareItems<T>(a: T, b: T, c: T): boolean {
    // Using strict equality for type safety
    if (a === b) {
        console.log('Items strictly match');
        return true;
    }
    
    if (b !== c) {
        console.log('Items strictly differ');
    }
    
    return false;
}

enum Status {
    ACTIVE = 'active',
    INACTIVE = 'inactive',
    PENDING = 'pending'
}

function checkUserStatus(user: User, expectedStatus: Status): boolean {
    // Strict equality with enum
    return user.status === expectedStatus;
}

// Type guards using strict equality
function isString(value: unknown): value is string {
    return typeof value === 'string';
}

function isNumber(value: unknown): value is number {
    return typeof value === 'number';
}

function isActiveUser(user: User): boolean {
    return user.status === Status.ACTIVE && user.age !== undefined && user.age > 0;
}

// Complex validation with strict equality
function validateUserPreferences(preferences: Record<string, any> | undefined): boolean {
    if (preferences === undefined) {
        return true; // No preferences is valid
    }
    
    for (const [key, value] of Object.entries(preferences)) {
        if (typeof key !== 'string' || key === '') {
            return false;
        }
        
        // Check for specific preference types
        if (key === 'theme' && value !== 'light' && value !== 'dark') {
            return false;
        }
        
        if (key === 'notifications' && value !== true && value !== false) {
            return false;
        }
    }
    
    return true;
}

export { 
    validateUserData, 
    ApiClient, 
    processApiData, 
    compareItems, 
    checkUserStatus,
    isString,
    isNumber,
    isActiveUser,
    validateUserPreferences
};