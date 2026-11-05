// TypeScript file with multiple console.log calls
interface User {
  id: number;
  name: string;
}

function processUser(user: User): void {
  console.log('Processing user:', user.name);
  
  if (user.id < 0) {
    console.log('Warning: Invalid user ID');
    return;
  }
  
  // Debug logging
  console.log(`User ${user.id} validation passed`);
  
  // More console.log calls
  console.log('User processing complete');
}

export { processUser };