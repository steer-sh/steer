"""
Examples of safe database operations using ORM and safe query methods
This should NOT be blocked by the hook
"""

from sqlalchemy import create_engine, Column, Integer, String, DateTime
from sqlalchemy.ext.declarative import declarative_base
from sqlalchemy.orm import sessionmaker
from django.contrib.auth.models import User
from django.db import models

Base = declarative_base()

class Product(Base):
    """SQLAlchemy model example"""
    __tablename__ = 'products'
    
    id = Column(Integer, primary_key=True)
    name = Column(String(100))
    price = Column(Integer)
    category = Column(String(50))

def get_products_sqlalchemy(session, category, min_price):
    """Good: Using SQLAlchemy ORM"""
    # ORM automatically handles parameterization
    return session.query(Product).filter(
        Product.category == category,
        Product.price >= min_price
    ).all()

def create_user_sqlalchemy(session, name, email):
    """Good: ORM insert operations"""
    # Safe ORM operations
    new_product = Product(name=name, price=100, category='electronics')
    session.add(new_product)
    session.commit()
    return new_product

def get_user_django(username):
    """Good: Django ORM query"""
    # Django ORM handles parameterization automatically
    try:
        return User.objects.get(username=username)
    except User.DoesNotExist:
        return None

def search_users_django(email_domain):
    """Good: Django ORM with filters"""
    # Safe filtering with Django ORM
    return User.objects.filter(email__endswith=f'@{email_domain}')

def safe_raw_query_django(user_id):
    """Good: Even raw queries can be safe with proper parameters"""
    # Django's raw() method supports parameterization
    return User.objects.raw("SELECT * FROM auth_user WHERE id = %s", [user_id])

def safe_query_builder():
    """Good: Using query builders safely"""
    from sqlalchemy import text
    
    engine = create_engine('sqlite:///example.db')
    
    # SQLAlchemy text() with bound parameters
    result = engine.execute(
        text("SELECT * FROM users WHERE age > :age AND city = :city"),
        age=25, city='New York'
    )
    return result.fetchall()

# Example of safe string operations (not SQL injection risks)
def format_user_message(username, message):
    """Good: Regular string formatting (not SQL)"""
    # This is just regular string formatting, not SQL - should be allowed
    return f"User {username} says: {message}"

def log_database_operation(table_name, operation):
    """Good: Logging with string formatting (not SQL)"""
    # Regular logging, not SQL queries
    log_message = f"Performed {operation} on table {table_name}"
    print(log_message)