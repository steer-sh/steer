// Package main demonstrates acceptable panic usage in main package
package main

import (
	"fmt"
	"log"
	"os"
)

func main() {
	// In main package, panic can be acceptable for unrecoverable errors
	configFile := os.Getenv("CONFIG_FILE")
	if configFile == "" {
		panic("CONFIG_FILE environment variable is required") // Acceptable in main
	}
	
	// Setup that must succeed for the program to continue
	if err := initializeApp(); err != nil {
		panic(fmt.Sprintf("failed to initialize app: %v", err)) // Acceptable in main
	}
	
	fmt.Println("Application started successfully")
}

func initializeApp() error {
	// Simulate initialization
	return nil
}

// Helper function in main package
func mustLoadConfig() map[string]string {
	// In main package, "must" functions with panic are more acceptable
	config := make(map[string]string)
	
	// Critical configuration that the app cannot run without
	if os.Getenv("CRITICAL_SETTING") == "" {
		panic("CRITICAL_SETTING is required") // Acceptable in main package
	}
	
	return config
}

// Another main package function where panic might be acceptable
func init() {
	// During initialization, panic for critical setup failures is acceptable
	if _, err := os.Stat("required_file.txt"); os.IsNotExist(err) {
		log.Println("Warning: required_file.txt not found")
		// Note: Even in main, consider whether panic is truly necessary
	}
}