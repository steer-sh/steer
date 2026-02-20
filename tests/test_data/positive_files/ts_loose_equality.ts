// TypeScript code using loose equality operators (should be BLOCKED)

interface User {
    id: number;
    name: string;
    email: string;
    age?: number;
    status: 'active' | 'inactive' | 'pending';
    preferences?: Record<string, any>;
}

function validateUserData(user: User): boolean {
    // Using loose equality - should trigger hook
    if (user.id == null) {
        return false;
    }
    
    // Loose equality with optional property
    if (user.age == undefined) {
        console.log('Age not provided');
    }
    
    // Loose inequality
    if (user.status != 'active') {
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
        
        // Loose equality with response status
        if (response.status == 200) {
            return response.json();
        }
        
        // Loose inequality
        if (response.status != 404) {
            console.error('Unexpected error');
        }
        
        return null;
    }
    
    validateResponse(data: any): boolean {
        // Multiple loose equality checks
        if (data.success == true && data.error == null) {
            return true;
        }
        
        return false;
    }
}

function processApiData(data: any[]): User[] {
    return data.filter((item: any) => {
        // Loose equality in filter
        return item.type == 'user' && item.active != false;
    }).map((item: any) => ({
        id: item.id,
        name: item.name,
        email: item.email,
        status: item.status,
        age: item.age
    }));
}

// Generic function with loose equality
function compareItems<T>(a: T, b: T, c: T): boolean {
    // TypeScript allows loose equality but we want to enforce strict
    if (a == b) {
        console.log('Items match');
        return true;
    }
    
    if (b != c) {
        console.log('Items differ');
    }
    
    return false;
}

enum Status {
    ACTIVE = 'active',
    INACTIVE = 'inactive',
    PENDING = 'pending'
}

function checkUserStatus(user: User, expectedStatus: Status): boolean {
    // Loose equality with enum
    return user.status == expectedStatus;
}

export { validateUserData, ApiClient, processApiData, compareItems, checkUserStatus };