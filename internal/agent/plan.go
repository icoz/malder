package agent

type VerbosityLevel int

const (
	VerbosityBrief VerbosityLevel = iota
	VerbosityNormal
	VerbosityDetailed
)

func (v VerbosityLevel) String() string {
	switch v {
	case VerbosityBrief:
		return "brief"
	case VerbosityNormal:
		return "normal"
	case VerbosityDetailed:
		return "detailed"
	default:
		return "normal"
	}
}

func ParseVerbosity(s string) VerbosityLevel {
	switch s {
	case "brief":
		return VerbosityBrief
	case "detailed":
		return VerbosityDetailed
	default:
		return VerbosityNormal
	}
}

type ResearchPlan struct {
	Title    string    `json:"title"`
	Sections []Section `json:"sections"`
}

type Section struct {
	Name      string     `json:"name"`
	Subtopics []Subtopic `json:"subtopics"`
}

type Subtopic struct {
	Name    string   `json:"name"`
	Queries []string `json:"queries"`
}
