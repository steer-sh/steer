/**
 * React component with bad data fetching patterns
 * This should be blocked by the hook
 */

import React, { useState } from 'react';

// Bad: fetch call directly in component body
const UserProfile = ({ userId }) => {
  const [user, setUser] = useState(null);
  const [loading, setLoading] = useState(true);

  // Bad: direct fetch in component body - runs on every render
  fetch(`/api/users/${userId}`)
    .then(response => response.json())
    .then(userData => {
      setUser(userData);
      setLoading(false);
    })
    .catch(error => {
      console.error('Error fetching user:', error);
      setLoading(false);
    });

  if (loading) {
    return <div>Loading...</div>;
  }

  return (
    <div>
      <h1>{user?.name}</h1>
      <p>{user?.email}</p>
    </div>
  );
};

// Bad: multiple fetch calls in component body
const Dashboard = () => {
  const [users, setUsers] = useState([]);
  const [posts, setPosts] = useState([]);
  const [analytics, setAnalytics] = useState(null);

  // Bad: multiple direct fetches
  fetch('/api/users').then(response => response.json()).then(setUsers);
  fetch('/api/posts').then(response => response.json()).then(setPosts);
  fetch('/api/analytics').then(response => response.json()).then(setAnalytics);

  return (
    <div>
      <h2>Dashboard</h2>
      <div>Users: {users.length}</div>
      <div>Posts: {posts.length}</div>
      <div>Analytics: {analytics?.pageViews}</div>
    </div>
  );
};

// Bad: fetch in event handler defined in component body
const ProductList = () => {
  const [products, setProducts] = useState([]);

  // Bad: fetch call directly in component body
  const loadProducts = () => {
    fetch('/api/products')
      .then(response => response.json())
      .then(setProducts);
  };
  
  // This causes the fetch to run immediately
  loadProducts();

  return (
    <div>
      {products.map(product => (
        <div key={product.id}>{product.name}</div>
      ))}
    </div>
  );
};

// Bad: async fetch in component body
const NewsComponent = async () => {
  const [news, setNews] = useState([]);

  // Bad: async fetch directly in component body
  try {
    const response = await fetch('/api/news');
    const newsData = await response.json();
    setNews(newsData);
  } catch (error) {
    console.error('Failed to fetch news:', error);
  }

  return (
    <div>
      {news.map(article => (
        <div key={article.id}>{article.title}</div>
      ))}
    </div>
  );
};

// Bad: conditional fetch in component body
const ConditionalFetch = ({ shouldFetch, endpoint }) => {
  const [data, setData] = useState(null);

  // Bad: conditional fetch outside useEffect
  if (shouldFetch) {
    fetch(endpoint)
      .then(response => response.json())
      .then(setData);
  }

  return <div>{data ? JSON.stringify(data) : 'No data'}</div>;
};

export default UserProfile;