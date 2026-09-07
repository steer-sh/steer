package styles

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

var (
	BrandPurple = lipgloss.Color("#A78BFA")
	White       = lipgloss.Color("#FFFFFF")

	Success = lipgloss.Color("#10B981")
	Warning = lipgloss.Color("#F59E0B")
	Error   = lipgloss.Color("#EF4444")
	Info    = lipgloss.Color("#06B6D4")
	Muted   = lipgloss.Color("#9CA3AF")
)

var (
	Bold = lipgloss.NewStyle().Bold(true)
	Dim  = lipgloss.NewStyle().Foreground(Muted)

	BrandText = lipgloss.NewStyle().
			Foreground(BrandPurple).
			Bold(true)

	SuccessText = lipgloss.NewStyle().Foreground(Success).Bold(true)
	WarningText = lipgloss.NewStyle().Foreground(Warning).Bold(true)
	ErrorText   = lipgloss.NewStyle().Foreground(Error).Bold(true)
	InfoText    = lipgloss.NewStyle().Foreground(Info)
	MutedText   = lipgloss.NewStyle().Foreground(Muted)
)

var (
	MainHeader = lipgloss.NewStyle().
			Foreground(BrandPurple).
			Bold(true).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(BrandPurple).
			Padding(0, 1)

	SectionHeader = lipgloss.NewStyle().
			Foreground(White).
			Bold(true).
			UnderlineSpaces(true).
			Underline(true)

	InfoBox = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Info).
		Padding(0, 1)

	SuccessBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(Success).
			Padding(0, 1)

	WarningBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(Warning).
			Padding(0, 1)

	ErrorBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(Error).
			Padding(0, 1)

	CodeBlock = lipgloss.NewStyle().
			Foreground(Muted).
			Italic(true)

	PathText = lipgloss.NewStyle().
			Foreground(Muted).
			Italic(true)
)

func Logo() string {
	return BrandText.Render("steer")
}

func StatusIndicator(status, message string) string {
	switch status {
	case "success":
		return SuccessText.Render("✅ " + message)
	case "warning":
		return WarningText.Render("⚠️  " + message)
	case "error":
		return ErrorText.Render("❌ " + message)
	case "info":
		return InfoText.Render("🔍 " + message)
	case "working":
		return BrandText.Render("🔧 " + message)
	case "saving":
		return SuccessText.Render("💾 " + message)
	case "registering":
		return SuccessText.Render("📝 " + message)
	case "tip":
		return BrandText.Render("💡 " + message)
	default:
		return message
	}
}

func CommandExample(command string) string {
	return Dim.Render("$ " + command)
}

func HookListItem(number int, title, path string) string {
	numberStyle := MutedText.Render(fmt.Sprintf("  %d. ", number))
	titleStyle := Bold.Render(title)
	pathStyle := PathText.Render(fmt.Sprintf("\n     Path: %s", path))
	return numberStyle + titleStyle + pathStyle
}

func HookListItemWithRule(number int, title, path, rule string) string {
	base := HookListItem(number, title, path)
	ruleStyle := InfoText.Render(fmt.Sprintf("\n     Rule: %s", rule))
	return base + ruleStyle
}
