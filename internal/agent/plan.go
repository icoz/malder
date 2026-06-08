package agent

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
