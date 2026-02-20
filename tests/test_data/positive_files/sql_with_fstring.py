"""
Examples of dangerous SQL injection vulnerabilities using f-strings
This should be blocked by the hook
"""

import sqlite3
import psycopg2

def get_user_by_id_unsafe(user_id):
    """Bad: Using f-string in SQL query"""
    connection = sqlite3.connect('database.db')
    cursor = connection.cursor()
    
    # Vulnerable to SQL injection
    query = f"SELECT * FROM users WHERE id = {user_id}"
    cursor.execute(query)
    return cursor.fetchall()

def search_products_unsafe(category, price_min):
    """Bad: Using f-string with user input"""
    conn = psycopg2.connect("dbname=shop")
    cur = conn.cursor()
    
    # Multiple injection points
    sql = f"SELECT * FROM products WHERE category = '{category}' AND price >= {price_min}"
    cur.execute(sql)
    return cur.fetchall()

def delete_user_unsafe(username):
    """Bad: f-string in DELETE statement"""
    conn = sqlite3.connect('users.db')
    cursor = conn.cursor()
    
    # Very dangerous - could delete all users
    query = f"DELETE FROM users WHERE username = '{username}'"
    cursor.execute(query)
    conn.commit()

def update_profile_unsafe(user_id, email, name):
    """Bad: Multiple f-string interpolations"""
    connection = sqlite3.connect('database.db')
    cursor = connection.cursor()
    
    # Multiple vulnerable parameters
    update_sql = f"UPDATE users SET email = '{email}', name = '{name}' WHERE id = {user_id}"
    cursor.execute(update_sql)
    connection.commit()

def complex_query_unsafe(table_name, column, value):
    """Bad: Even dynamic table/column names with f-strings are dangerous"""
    conn = sqlite3.connect('db.sqlite')
    cur = conn.cursor()
    
    # Dynamic table and column names - very dangerous
    query = f"SELECT * FROM {table_name} WHERE {column} = '{value}'"
    cur.execute(query)
    return cur.fetchall()