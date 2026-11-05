/**
 * React component with proper data fetching using useEffect
 * This should NOT be blocked by the hook
 */

import React, { useState, useEffect, useCallback } from 'react';

// Good: fetch call inside useEffect
const UserProfile = ({ userId }) => {
  const [user, setUser] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);

  useEffect(() => {
    // Good: fetch inside useEffect
    const fetchUser = async () => {
      try {
        setLoading(true);
        const response = await fetch(`/api/users/${userId}`);
        if (!response.ok) {
          throw new Error('Failed to fetch user');
        }
        const userData = await response.json();
        setUser(userData);
      } catch (err) {
        setError(err.message);
      } finally {
        setLoading(false);
      }
    };

    fetchUser();
  }, [userId]);

  if (loading) return <div>Loading...</div>;
  if (error) return <div>Error: {error}</div>;

  return (
    <div>
      <h1>{user?.name}</h1>
      <p>{user?.email}</p>
    </div>
  );
};

// Good: multiple fetch calls properly managed in useEffect
const Dashboard = () => {
  const [users, setUsers] = useState([]);
  const [posts, setPosts] = useState([]);
  const [analytics, setAnalytics] = useState(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    // Good: all fetches inside useEffect
    const fetchData = async () => {
      try {
        const [usersResponse, postsResponse, analyticsResponse] = await Promise.all([
          fetch('/api/users'),
          fetch('/api/posts'),
          fetch('/api/analytics')
        ]);

        const [usersData, postsData, analyticsData] = await Promise.all([
          usersResponse.json(),
          postsResponse.json(),
          analyticsResponse.json()
        ]);

        setUsers(usersData);
        setPosts(postsData);
        setAnalytics(analyticsData);
      } catch (error) {
        console.error('Error fetching dashboard data:', error);
      } finally {
        setLoading(false);
      }
    };

    fetchData();
  }, []);

  if (loading) return <div>Loading dashboard...</div>;

  return (
    <div>
      <h2>Dashboard</h2>
      <div>Users: {users.length}</div>
      <div>Posts: {posts.length}</div>
      <div>Analytics: {analytics?.pageViews}</div>
    </div>
  );
};

// Good: fetch triggered by user action
const ProductList = () => {
  const [products, setProducts] = useState([]);
  const [loading, setLoading] = useState(false);

  const loadProducts = useCallback(async () => {
    // Good: fetch in callback function, not component body
    setLoading(true);
    try {
      const response = await fetch('/api/products');
      const productsData = await response.json();
      setProducts(productsData);
    } catch (error) {
      console.error('Error loading products:', error);
    } finally {
      setLoading(false);
    }
  }, []);

  // Good: fetch on mount using useEffect
  useEffect(() => {
    loadProducts();
  }, [loadProducts]);

  return (
    <div>
      <button onClick={loadProducts} disabled={loading}>
        {loading ? 'Loading...' : 'Refresh Products'}
      </button>
      {products.map(product => (
        <div key={product.id}>{product.name}</div>
      ))}
    </div>
  );
};

// Good: conditional fetch properly handled in useEffect
const ConditionalFetch = ({ shouldFetch, endpoint }) => {
  const [data, setData] = useState(null);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    // Good: conditional fetch inside useEffect
    if (shouldFetch && endpoint) {
      const fetchData = async () => {
        setLoading(true);
        try {
          const response = await fetch(endpoint);
          const result = await response.json();
          setData(result);
        } catch (error) {
          console.error('Fetch error:', error);
        } finally {
          setLoading(false);
        }
      };

      fetchData();
    }
  }, [shouldFetch, endpoint]);

  if (loading) return <div>Loading...</div>;
  return <div>{data ? JSON.stringify(data) : 'No data'}</div>;
};

// Good: cleanup and abort controller
const NewsComponent = () => {
  const [news, setNews] = useState([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    // Good: using AbortController for cleanup
    const controller = new AbortController();

    const fetchNews = async () => {
      try {
        const response = await fetch('/api/news', {
          signal: controller.signal
        });
        const newsData = await response.json();
        setNews(newsData);
      } catch (error) {
        if (error.name !== 'AbortError') {
          console.error('Failed to fetch news:', error);
        }
      } finally {
        setLoading(false);
      }
    };

    fetchNews();

    // Good: cleanup function
    return () => {
      controller.abort();
    };
  }, []);

  if (loading) return <div>Loading news...</div>;

  return (
    <div>
      {news.map(article => (
        <div key={article.id}>
          <h3>{article.title}</h3>
          <p>{article.summary}</p>
        </div>
      ))}
    </div>
  );
};

// Good: debounced search with useEffect
const SearchComponent = () => {
  const [searchTerm, setSearchTerm] = useState('');
  const [results, setResults] = useState([]);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    // Good: debounced fetch in useEffect
    if (!searchTerm) {
      setResults([]);
      return;
    }

    const timeoutId = setTimeout(async () => {
      setLoading(true);
      try {
        const response = await fetch(`/api/search?q=${encodeURIComponent(searchTerm)}`);
        const searchResults = await response.json();
        setResults(searchResults);
      } catch (error) {
        console.error('Search error:', error);
      } finally {
        setLoading(false);
      }
    }, 300);

    return () => clearTimeout(timeoutId);
  }, [searchTerm]);

  return (
    <div>
      <input
        type="text"
        value={searchTerm}
        onChange={(e) => setSearchTerm(e.target.value)}
        placeholder="Search..."
      />
      {loading && <div>Searching...</div>}
      {results.map(result => (
        <div key={result.id}>{result.title}</div>
      ))}
    </div>
  );
};

export default UserProfile;