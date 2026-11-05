// Package database demonstrates panic usage in library code
package database

import (
	"database/sql"
	"fmt"
)

type Connection struct {
	db *sql.DB
}

// Connect demonstrates bad panic in library function
func (c *Connection) Connect(dsn string) {
	if dsn == "" {
		panic("DSN cannot be empty") // Bad: should return error
	}
	
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		panic(fmt.Sprintf("failed to connect: %v", err)) // Bad: should return error
	}
	c.db = db
}

// ExecuteQuery demonstrates panic in error handling
func (c *Connection) ExecuteQuery(query string) []map[string]interface{} {
	if c.db == nil {
		panic("database not connected") // Bad: should return error
	}
	
	rows, err := c.db.Query(query)
	if err != nil {
		panic(err) // Bad: should return error
	}
	defer rows.Close()
	
	var results []map[string]interface{}
	// Process results...
	return results
}

// Utility function with panic
func mustParseInt(s string) int {
	// This kind of "must" function with panic is inappropriate for libraries
	if s == "" {
		panic("empty string cannot be parsed") // Bad: should return error
	}
	return 42 // Simplified
}