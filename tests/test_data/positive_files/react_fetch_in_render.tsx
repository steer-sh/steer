/**
 * React component with fetch calls during render
 * This should be blocked by the hook
 */

import React, { useState, FC } from 'react';

interface User {
  id: number;
  name: string;
  email: string;
}

interface Post {
  id: number;
  title: string;
  content: string;
}

// Bad: fetch call in render method
const UserCard: FC<{ userId: number }> = ({ userId }) => {
  const [user, setUser] = useState<User | null>(null);

  // Bad: fetch during render - causes infinite loop
  if (!user) {
    fetch(`/api/users/${userId}`)
      .then(response => response.json())
      .then((userData: User) => setUser(userData));
  }

  return (
    <div className="user-card">
      <h3>{user?.name || 'Loading...'}</h3>
      <p>{user?.email}</p>
    </div>
  );
};

// Bad: fetch in JSX expression
const PostList: FC = () => {
  const [posts, setPosts] = useState<Post[]>([]);

  return (
    <div>
      <h2>Posts</h2>
      {/* Bad: fetch call inside JSX */}
      {posts.length === 0 && 
        fetch('/api/posts')
          .then(response => response.json())
          .then((postsData: Post[]) => setPosts(postsData))
      }
      {posts.map(post => (
        <div key={post.id}>
          <h4>{post.title}</h4>
          <p>{post.content}</p>
        </div>
      ))}
    </div>
  );
};

// Bad: fetch in computed value during render
const StatsComponent: FC = () => {
  const [stats, setStats] = useState({ views: 0, likes: 0 });

  // Bad: fetch in computed property
  const loadStats = () => {
    fetch('/api/stats')
      .then(response => response.json())
      .then(setStats);
    return 'Loading...';
  };

  return (
    <div>
      <h2>Statistics</h2>
      <p>Views: {stats.views}</p>
      <p>Likes: {stats.likes}</p>
      {/* Bad: calling function that fetches during render */}
      <p>Status: {loadStats()}</p>
    </div>
  );
};

// Bad: fetch in render with ternary operator
const ProfileComponent: FC<{ profileId: string }> = ({ profileId }) => {
  const [profile, setProfile] = useState(null);
  const [loading, setLoading] = useState(true);

  return (
    <div>
      {/* Bad: fetch call in ternary expression */}
      {loading ? (
        fetch(`/api/profile/${profileId}`)
          .then(response => response.json())
          .then(profileData => {
            setProfile(profileData);
            setLoading(false);
          }) && <div>Loading profile...</div>
      ) : (
        <div>
          <h2>{profile?.name}</h2>
          <p>{profile?.bio}</p>
        </div>
      )}
    </div>
  );
};

// Bad: fetch in map function during render
const TeamMembers: FC = () => {
  const [teamIds] = useState([1, 2, 3, 4]);
  const [members, setMembers] = useState<User[]>([]);

  return (
    <div>
      <h2>Team Members</h2>
      {teamIds.map(id => {
        // Bad: fetch call inside map during render
        fetch(`/api/users/${id}`)
          .then(response => response.json())
          .then((user: User) => {
            setMembers(prev => [...prev, user]);
          });

        return <div key={id}>Loading member {id}...</div>;
      })}
    </div>
  );
};

// Bad: fetch in return statement
const WeatherWidget: FC<{ city: string }> = ({ city }) => {
  const [weather, setWeather] = useState(null);

  return (
    <div className="weather-widget">
      <h3>Weather for {city}</h3>
      {/* Bad: fetch directly in return statement */}
      {!weather && (
        fetch(`/api/weather/${city}`)
          .then(response => response.json())
          .then(setWeather),
        <div>Fetching weather...</div>
      )}
      {weather && (
        <div>
          <p>Temperature: {weather.temperature}°C</p>
          <p>Condition: {weather.condition}</p>
        </div>
      )}
    </div>
  );
};

export default UserCard;