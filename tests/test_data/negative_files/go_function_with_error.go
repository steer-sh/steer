// Package utils demonstrates proper error handling in Go
package utils

import (
	"errors"
	"fmt"
)

// ProcessData demonstrates proper error returns instead of panic
func ProcessData(data []string) ([]string, error) {
	if len(data) == 0 {
		return nil, errors.New("data cannot be empty") // Good: return error
	}
	
	result := make([]string, len(data))
	for i, item := range data {
		if item == "" {
			return nil, fmt.Errorf("empty item found at index %d", i) // Good: return error
		}
		result[i] = fmt.Sprintf("processed_%s", item)
	}
	return result, nil
}

// ParseConfig demonstrates proper error handling
func ParseConfig(configPath string) (map[string]string, error) {
	if configPath == "" {
		return nil, errors.New("config path is required") // Good: return error
	}
	
	// Simulate config parsing
	config := make(map[string]string)
	if configPath == "invalid.json" {
		return nil, errors.New("failed to parse config") // Good: return error
	}
	
	return config, nil
}

// Helper function with proper error return
func validateInput(input string) error {
	if input == "" {
		return errors.New("input validation failed") // Good: return error
	}
	return nil
}

// Example of using the error-returning functions
func ExampleUsage() error {
	data := []string{"item1", "item2"}
	processed, err := ProcessData(data)
	if err != nil {
		return fmt.Errorf("failed to process data: %w", err)
	}
	
	fmt.Printf("Processed: %v\n", processed)
	return nil
}