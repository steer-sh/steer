// TypeScript switch statement without default case (should be BLOCKED)

enum UserRole {
    ADMIN = 'admin',
    USER = 'user',
    MODERATOR = 'moderator'
}

function getUserPermissions(role: UserRole): string[] {
    let permissions: string[] = [];
    
    switch (role) {
        case UserRole.ADMIN:
            permissions = ['read', 'write', 'delete', 'manage'];
            break;
        case UserRole.MODERATOR:
            permissions = ['read', 'write', 'moderate'];
            break;
        case UserRole.USER:
            permissions = ['read'];
            break;
        // Missing default case - this should trigger the hook
    }
    
    return permissions;
}

interface ApiResponse {
    status: number;
    data: any;
}

function handleApiResponse(response: ApiResponse): void {
    switch (response.status) {
        case 200:
            console.log('Success:', response.data);
            break;
        case 400:
            console.error('Bad request');
            break;
        case 401:
            console.error('Unauthorized');
            break;
        // No default case here either
    }
}

export { getUserPermissions, handleApiResponse };