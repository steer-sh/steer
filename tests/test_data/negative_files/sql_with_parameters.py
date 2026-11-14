"""
Examples of safe SQL queries using parameterized queries
This should NOT be blocked by the hook
"""

import sqlite3
import psycopg2

def get_user_by_id_safe(user_id):
    """Good: Using parameterized query with ?"""
    connection = sqlite3.connect('database.db')
    cursor = connection.cursor()
    
    # Safe parameterized query
    query = "SELECT * FROM users WHERE id = ?"
    cursor.execute(query, (user_id,))
    return cursor.fetchall()

def search_products_safe(category, price_min):
    """Good: Using parameterized query with named parameters"""
    conn = psycopg2.connect("dbname=shop")
    cur = conn.cursor()
    
    # Safe with parameters
    sql = "SELECT * FROM products WHERE category = %s AND price >= %s"
    cur.execute(sql, (category, price_min))
    return cur.fetchall()

def delete_user_safe(username):
    """Good: Parameterized DELETE statement"""
    conn = sqlite3.connect('users.db')
    cursor = conn.cursor()
    
    # Safe deletion
    query = "DELETE FROM users WHERE username = ?"
    cursor.execute(query, (username,))
    conn.commit()

def update_profile_safe(user_id, email, name):
    """Good: Parameterized UPDATE with multiple parameters"""
    connection = sqlite3.connect('database.db')
    cursor = connection.cursor()
    
    # Safe update with parameters
    update_sql = "UPDATE users SET email = ?, name = ? WHERE id = ?"
    cursor.execute(update_sql, (email, name, user_id))
    connection.commit()

def authenticate_user_safe(username, password):
    """Good: Safe authentication with parameters"""
    connection = sqlite3.connect('users.db')
    cursor = connection.cursor()
    
    # Safe authentication
    query = "SELECT * FROM users WHERE username = ? AND password = ?"
    cursor.execute(query, (username, password))
    return cursor.fetchone() is not None

def get_orders_safe(customer_id, status):
    """Good: Multiple parameters safely handled"""
    conn = psycopg2.connect("dbname=shop")
    cursor = conn.cursor()
    
    # Safe with proper parameterization
    sql = "SELECT * FROM orders WHERE customer_id = %s AND status = %s"
    cursor.execute(sql, (customer_id, status))
    return cursor.fetchall()

def bulk_insert_safe(records):
    """Good: Safe bulk operations with executemany"""
    connection = sqlite3.connect('data.db')
    cursor = connection.cursor()
    
    # Safe bulk insert
    insert_sql = "INSERT INTO products (name, price, category) VALUES (?, ?, ?)"
    data = [(record['name'], record['price'], record['category']) for record in records]
    cursor.executemany(insert_sql, data)
    connection.commit()