// Package stubcall is the record the stub reviewer logs for each call.
package stubcall

// Call is one stub reviewer invocation.
type Call struct {
	Provider string   `json:"provider"`
	Argv     []string `json:"argv"`
	Stdin    string   `json:"stdin"`
	Prompt   string   `json:"prompt"`
	Model    string   `json:"model"`
	Effort   string   `json:"effort"`
	Cwd      string   `json:"cwd"`
	Index    int      `json:"index"`
	Mode     string   `json:"mode"`
}
