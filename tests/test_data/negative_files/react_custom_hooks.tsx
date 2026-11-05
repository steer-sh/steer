/**
 * React component using custom hooks for data fetching
 * This should NOT be blocked by the hook
 */

import React, { useState, useEffect, useCallback } from 'react';

// Good: Custom hook that encapsulates fetch logic
const useApi = <T>(url: string) => {
  const [data, setData] = useState<T | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    // Good: fetch inside custom hook's useEffect
    const fetchData = async () => {
      try {
        setLoading(true);
        setError(null);
        const response = await fetch(url);
        
        if (!response.ok) {
          throw new Error(`HTTP error! status: ${response.status}`);
        }
        
        const result = await response.json();
        setData(result);
      } catch (err) {
        setError(err instanceof Error ? err.message : 'An error occurred');
      } finally {
        setLoading(false);
      }
    };

    fetchData();
  }, [url]);

  return { data, loading, error };
};

// Good: Custom hook for user data
const useUser = (userId: number) => {
  const [user, setUser] = useState(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    // Good: fetch in custom hook
    const fetchUser = async () => {
      setLoading(true);
      try {
        const response = await fetch(`/api/users/${userId}`);
        const userData = await response.json();
        setUser(userData);
      } finally {
        setLoading(false);
      }
    };

    fetchUser();
  }, [userId]);

  return { user, loading };
};

// Good: Custom hook with mutation capabilities
const useUsers = () => {
  const [users, setUsers] = useState([]);
  const [loading, setLoading] = useState(false);

  const fetchUsers = useCallback(async () => {
    // Good: fetch inside custom hook method
    setLoading(true);
    try {
      const response = await fetch('/api/users');
      const usersData = await response.json();
      setUsers(usersData);
    } finally {
      setLoading(false);
    }
  }, []);

  const createUser = useCallback(async (userData) => {
    // Good: POST request in custom hook
    try {
      const response = await fetch('/api/users', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(userData)
      });
      const newUser = await response.json();
      setUsers(prev => [...prev, newUser]);
      return newUser;
    } catch (error) {
      console.error('Error creating user:', error);
      throw error;
    }
  }, []);

  useEffect(() => {
    fetchUsers();
  }, [fetchUsers]);

  return { users, loading, createUser, refetch: fetchUsers };
};

// Good: Component using custom hooks (no direct fetch calls)
const UserProfile: React.FC<{ userId: number }> = ({ userId }) => {
  const { user, loading } = useUser(userId);

  if (loading) return <div>Loading...</div>;

  return (
    <div>
      <h1>{user?.name}</h1>
      <p>{user?.email}</p>
    </div>
  );
};

// Good: Component using generic custom hook
const PostsList: React.FC = () => {
  const { data: posts, loading, error } = useApi<any[]>('/api/posts');

  if (loading) return <div>Loading posts...</div>;
  if (error) return <div>Error: {error}</div>;

  return (
    <div>
      <h2>Posts</h2>
      {posts?.map(post => (
        <div key={post.id}>
          <h3>{post.title}</h3>
          <p>{post.content}</p>
        </div>
      ))}
    </div>
  );
};

// Good: Component with mutation operations
const UserManagement: React.FC = () => {
  const { users, loading, createUser } = useUsers();
  const [newUserName, setNewUserName] = useState('');

  const handleCreateUser = async () => {
    if (!newUserName.trim()) return;
    
    try {
      await createUser({ name: newUserName, email: `${newUserName}@example.com` });
      setNewUserName('');
    } catch (error) {
      alert('Failed to create user');
    }
  };

  return (
    <div>
      <h2>User Management</h2>
      
      <div>
        <input
          type="text"
          value={newUserName}
          onChange={(e) => setNewUserName(e.target.value)}
          placeholder="User name"
        />
        <button onClick={handleCreateUser}>Create User</button>
      </div>

      {loading ? (
        <div>Loading users...</div>
      ) : (
        <div>
          {users.map(user => (
            <div key={user.id}>
              <strong>{user.name}</strong> - {user.email}
            </div>
          ))}
        </div>
      )}
    </div>
  );
};

// Good: Custom hook with SWR-like functionality
const useApiWithCache = <T>(key: string, fetcher: () => Promise<T>) => {
  const [data, setData] = useState<T | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    // Good: abstracted fetch logic in custom hook
    let isCancelled = false;

    const fetchData = async () => {
      try {
        const result = await fetcher();
        if (!isCancelled) {
          setData(result);
        }
      } finally {
        if (!isCancelled) {
          setLoading(false);
        }
      }
    };

    fetchData();

    return () => {
      isCancelled = true;
    };
  }, [key, fetcher]);

  return { data, loading };
};

// Good: Component using abstracted fetch
const Dashboard: React.FC = () => {
  const { data: stats, loading } = useApiWithCache('dashboard-stats', async () => {
    // Good: fetch call abstracted away from component
    const response = await fetch('/api/dashboard/stats');
    return response.json();
  });

  if (loading) return <div>Loading dashboard...</div>;

  return (
    <div>
      <h2>Dashboard</h2>
      <div>Page Views: {stats?.pageViews}</div>
      <div>Active Users: {stats?.activeUsers}</div>
    </div>
  );
};

export default UserProfile;