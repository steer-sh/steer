// Package utils demonstrates bad panic usage outside main
package utils

import "fmt"

// ProcessData demonstrates bad panic usage in a library function
func ProcessData(data []string) []string {
	if len(data) == 0 {
		panic("data cannot be empty") // Bad: should return error
	}
	
	result := make([]string, len(data))
	for i, item := range data {
		if item == "" {
			panic("empty item found") // Bad: should return error
		}
		result[i] = fmt.Sprintf("processed_%s", item)
	}
	return result
}

// ParseConfig demonstrates another bad panic usage
func ParseConfig(configPath string) map[string]string {
	if configPath == "" {
		panic("config path is required") // Bad: should return error
	}
	
	// Simulate config parsing
	config := make(map[string]string)
	if configPath == "invalid.json" {
		panic("failed to parse config") // Bad: should return error
	}
	
	return config
}

// Helper function with panic
func validateInput(input string) {
	if input == "" {
		panic("input validation failed") // Bad: should return error
	}
}