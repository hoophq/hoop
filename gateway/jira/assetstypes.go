package jira

import "time"

type AqlResponse struct {
	StartAt        int  `json:"startAt"`
	MaxResults     int  `json:"maxResults"`
	Total          int  `json:"total"`
	Last           bool `json:"last"`
	HasMoreResults bool `json:"hasMoreResults"`

	Values []AqlResponseValue `json:"values"`
}

type AqlResponseValue struct {
	Name       string     `json:"name"`
	GlobalID   string     `json:"globalId"`
	ID         string     `json:"id"`
	Label      string     `json:"label"`
	ObjectKey  string     `json:"objectKey"`
	ObjectType ObjectType `json:"objectType"`
	UpdatedAt  time.Time  `json:"updated"`
	CreatedAt  time.Time  `json:"created"`
}

type ObjectType struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Type           int    `json:"type"`
	Description    string `json:"description"`
	ObjectSchemaID string `json:"objectSchemaId"`
}
