"""
Examples of dangerous SQL injection vulnerabilities using %-formatting
This should be blocked by the hook
"""

import sqlite3
import mysql.connector

def authenticate_user_unsafe(username, password):
    """Bad: Using % formatting in authentication query"""
    connection = sqlite3.connect('users.db')
    cursor = connection.cursor()
    
    # Classic SQL injection vulnerability
    query = "SELECT * FROM users WHERE username = '%s' AND password = '%s'" % (username, password)
    cursor.execute(query)
    return cursor.fetchone() is not None

def get_orders_unsafe(customer_id, status):
    """Bad: Using % formatting with multiple parameters"""
    conn = mysql.connector.connect(user='user', password='pass', database='shop')
    cursor = conn.cursor()
    
    # Vulnerable to injection
    sql = "SELECT * FROM orders WHERE customer_id = %d AND status = '%s'" % (customer_id, status)
    cursor.execute(sql)
    return cursor.fetchall()

def search_logs_unsafe(start_date, end_date, level):
    """Bad: Date parameters with % formatting"""
    connection = sqlite3.connect('logs.db')
    cursor = connection.cursor()
    
    # Date injection possible
    query = "SELECT * FROM logs WHERE date BETWEEN '%s' AND '%s' AND level = '%s'" % (start_date, end_date, level)
    cursor.execute(query)
    return cursor.fetchall()

def admin_query_unsafe(table, condition):
    """Bad: Administrative queries with % formatting"""
    conn = sqlite3.connect('admin.db')
    cur = conn.cursor()
    
    # Extremely dangerous - could access any table
    admin_sql = "SELECT COUNT(*) FROM %s WHERE %s" % (table, condition)
    cur.execute(admin_sql)
    return cur.fetchone()[0]

def bulk_insert_unsafe(records):
    """Bad: Bulk operations with % formatting"""
    connection = sqlite3.connect('data.db')
    cursor = connection.cursor()
    
    for record in records:
        # Each insert is vulnerable
        insert_sql = "INSERT INTO products (name, price, category) VALUES ('%s', %f, '%s')" % (
            record['name'], record['price'], record['category']
        )
        cursor.execute(insert_sql)
    
    connection.commit()