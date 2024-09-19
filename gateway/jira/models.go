package jira

// Estrutura para a criação de uma issue
type Issue struct {
	Fields IssueFields `json:"fields"`
}

type IssueFields struct {
	Project     Project     `json:"project"`
	Summary     string      `json:"summary"`
	Description interface{} `json:"description"`
	Issuetype   Issuetype   `json:"issuetype"`
}

type Project struct {
	Key string `json:"key"`
}

type Issuetype struct {
	Name string `json:"name"`
}
