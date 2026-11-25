// Modern JavaScript without var
const users = [];

const addUser = (name, age) => {
  const user = { name, age };
  users.push(user);
  return user;
};

class UserManager {
  constructor() {
    this.users = new Map();
  }
  
  addUser(user) {
    const id = Date.now();
    this.users.set(id, { ...user, id });
    return id;
  }
  
  getUser(id) {
    return this.users.get(id);
  }
}

const manager = new UserManager();
export { manager, addUser };