package jira

import (
	"fmt"
	"os"
)

func TextBlock(text string) map[string]interface{} {
	return map[string]interface{}{
		"type": "text",
		"text": text,
	}
}

func StrongTextBlock(text string) map[string]interface{} {
	return map[string]interface{}{
		"type": "text",
		"text": text,
		"marks": []interface{}{
			map[string]interface{}{
				"type": "strong",
			},
		},
	}
}

func CodeTextBlock(text string) map[string]interface{} {
	return map[string]interface{}{
		"type": "text",
		"text": text,
		"marks": []interface{}{
			map[string]interface{}{
				"type": "code",
			},
		},
	}
}

func CodeSnippetBlock(code string) map[string]interface{} {
	return map[string]interface{}{
		"type": "codeBlock",
		"content": []interface{}{
			map[string]interface{}{
				"type": "text",
				"text": code,
			},
		},
	}
}

func LinkBlock(text, url string) map[string]interface{} {
	return map[string]interface{}{
		"type": "text",
		"text": text,
		"marks": []map[string]interface{}{
			{
				"type": "link",
				"attrs": map[string]interface{}{
					"href": url,
				},
			},
		},
	}
}

func DividerBlock() map[string]interface{} {
	return map[string]interface{}{
		"type": "rule",
	}
}

func ParagraphBlock(content ...map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"type":    "paragraph",
		"content": content,
	}
}

func BuildIssue(projectKey, summary, issueType string, content []interface{}) Issue {
	return Issue{
		Fields: IssueFields{
			Project:   Project{Key: projectKey},
			Summary:   summary,
			Issuetype: Issuetype{Name: issueType},
			Description: map[string]interface{}{
				"type":    "doc",
				"version": 1,
				"content": content,
			},
		},
	}
}

func BuildUpdateIssue(currentIssueContent, newIssueContent []interface{}) map[string]interface{} {
	content := append(currentIssueContent, newIssueContent...)

	return map[string]interface{}{
		"update": map[string]interface{}{
			"description": []interface{}{
				map[string]interface{}{
					"set": map[string]interface{}{
						"type":    "doc",
						"version": 1,
						"content": content,
					},
				},
			},
		},
	}
}

func AppendWithDivider(currentIssueContent, newIssueContent []interface{}) map[string]interface{} {
	newContent := append([]interface{}{
		map[string]interface{}{
			"type": "rule", // Divider
		},
	}, newIssueContent...)

	content := BuildUpdateIssue(currentIssueContent, newContent)

	return content
}

type CreateSessionJiraIssueTemplate struct {
	UserName       string
	ConnectionName string
	SessionID      string
	SessionScript  string
}

func createSessionJiraIssueTemplate(
	projectKey,
	summary,
	issueType string,
	contentInfos CreateSessionJiraIssueTemplate,
) Issue {
	descriptionContent := []interface{}{
		ParagraphBlock(
			StrongTextBlock("user: "),
			TextBlock(contentInfos.UserName),
		),
		ParagraphBlock(
			StrongTextBlock("connection: "),
			TextBlock(contentInfos.ConnectionName),
		),
	}

	descriptionContent = append(descriptionContent,
		ParagraphBlock(
			StrongTextBlock("script: "),
		),
		CodeSnippetBlock(contentInfos.SessionScript),
	)

	descriptionContent = append(descriptionContent,
		ParagraphBlock(
			StrongTextBlock("session link: "),
			LinkBlock(
				fmt.Sprintf("%v/sessions/%v", os.Getenv("API_URL"), contentInfos.SessionID),
				fmt.Sprintf("%v/sessions/%v", os.Getenv("API_URL"), contentInfos.SessionID),
			),
		),
	)

	return BuildIssue(
		projectKey,
		summary,
		issueType,
		descriptionContent,
	)
}

type UpdateReviewJiraIssueTemplate struct {
	SessionID string
}

func updateReviewJiraIssueTemplate(
	currentIssueContent []interface{},
	newContentInfos UpdateReviewJiraIssueTemplate,
) map[string]interface{} {
	descriptionContent := []interface{}{
		ParagraphBlock(
			TextBlock("This session requires review, access the following link to see it: \n"),
		),
		ParagraphBlock(
			LinkBlock(
				fmt.Sprintf("%v/sessions/%v", os.Getenv("API_URL"), newContentInfos.SessionID),
				fmt.Sprintf("%v/sessions/%v", os.Getenv("API_URL"), newContentInfos.SessionID),
			)),
	}

	// Append new content below a divider
	return AppendWithDivider(currentIssueContent, descriptionContent)
}

type AddNewReviewByUserIssueTemplate struct {
	ReviewerName         string
	ReviewApprovedGroups string
	ReviewStatus         string
}

func addNewReviewByUserIssueTemplate(
	currentIssueContent []interface{},
	newContentInfos AddNewReviewByUserIssueTemplate,
) map[string]interface{} {
	descriptionContent := []interface{}{
		ParagraphBlock(
			TextBlock(newContentInfos.ReviewerName),
			TextBlock(" from "),
			StrongTextBlock(newContentInfos.ReviewApprovedGroups),
			TextBlock(" has "),
			StrongTextBlock(newContentInfos.ReviewStatus),
			TextBlock(" the session"),
		),
	}

	// Append new content below a divider
	return AppendWithDivider(currentIssueContent, descriptionContent)
}

type AddReviewReadyIssueTemplate struct {
	SessionID string
	ApiURL    string
}

func addReviewReadyIssueTemplate(
	currentIssueContent []interface{},
	newContentInfos AddReviewReadyIssueTemplate,
) map[string]interface{} {
	descriptionContent := []interface{}{
		ParagraphBlock(
			TextBlock("The session is ready to be executed by the link:"),
		),
		ParagraphBlock(
			LinkBlock(
				fmt.Sprintf("%v/sessions/%v", newContentInfos.ApiURL, newContentInfos.SessionID),
				fmt.Sprintf("%v/sessions/%v", newContentInfos.ApiURL, newContentInfos.SessionID),
			)),
	}

	// Append new content below a divider
	return AppendWithDivider(currentIssueContent, descriptionContent)
}

func addReviewRejectedOrRevokedIssueTemplate(
	currentIssueContent []interface{},
	status string,
) map[string]interface{} {
	descriptionContent := []interface{}{
		ParagraphBlock(
			TextBlock(fmt.Sprintf("The session was %v", status)),
		),
	}

	// Append new content below a divider
	return AppendWithDivider(currentIssueContent, descriptionContent)
}

type AddSessionExecutedIssueTemplate struct {
	Payload string
}

func addSessionExecutedIssueTemplate(
	currentIssueContent []interface{},
	newContentInfo AddSessionExecutedIssueTemplate,
) map[string]interface{} {
	descriptionContent := []interface{}{
		ParagraphBlock(
			TextBlock("The session was executed with the response: \n"),
		),
		CodeSnippetBlock(newContentInfo.Payload),
	}

	// Append new content below a divider
	return AppendWithDivider(currentIssueContent, descriptionContent)
}
