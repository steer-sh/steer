// TypeScript switch statement with default case (should be ALLOWED)

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
        default:
            console.warn('Unknown role:', role);
            permissions = ['read']; // Default to minimal permissions
            break;
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
        default:
            console.error('Unexpected status code:', response.status);
            break;
    }
}

type Theme = 'light' | 'dark' | 'auto';

function applyTheme(theme: Theme): void {
    switch (theme) {
        case 'light':
            document.body.className = 'theme-light';
            break;
        case 'dark':
            document.body.className = 'theme-dark';
            break;
        case 'auto':
            const prefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
            document.body.className = prefersDark ? 'theme-dark' : 'theme-light';
            break;
        default:
            // TypeScript should prevent this, but good practice to handle it
            console.error('Invalid theme:', theme);
            document.body.className = 'theme-light';
            break;
    }
}

export { getUserPermissions, handleApiResponse, applyTheme };