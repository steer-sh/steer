package utils

import (
	"fmt"

	"github.com/steer-sh/steer-cli/internal/styles"
)

func Success(message string) {
	fmt.Println(styles.StatusIndicator("success", message))
}

func Info(message string) {
	fmt.Println(styles.StatusIndicator("info", message))
}

func Working(message string) {
	fmt.Println(styles.StatusIndicator("working", message))
}

func Saving(message string) {
	fmt.Println(styles.StatusIndicator("saving", message))
}

func Registering(message string) {
	fmt.Println(styles.StatusIndicator("registering", message))
}

func Error(message string) {
	fmt.Println(styles.StatusIndicator("error", message))
}

func Warning(message string) {
	fmt.Println(styles.StatusIndicator("warning", message))
}

func Tip(message string) {
	fmt.Println(styles.StatusIndicator("tip", message))
}
