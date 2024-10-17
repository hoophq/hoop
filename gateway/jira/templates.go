package jira

import (
	"fmt"
	"os"
)

func textBlock(text string) map[string]interface{} {
	return map[string]interface{}{
		"type": "text",
		"text": text,
	}
}

func strongTextBlock(text string) map[string]interface{} {
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

func codeSnippetBlock(code string) map[string]interface{} {
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

func linkBlock(text, url string) map[string]interface{} {
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

func dividerBlock() map[string]interface{} {
	return map[string]interface{}{
		"type": "rule",
	}
}

func paragraphBlock(content ...map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"type":    "paragraph",
		"content": content,
	}
}

func buildIssue(projectKey, summary, issueType string, content []interface{}) Issue {
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

func buildUpdateIssue(currentIssueContent, newIssueContent []interface{}) map[string]interface{} {
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

func appendWithDivider(currentIssueContent, newIssueContent []interface{}) map[string]interface{} {
	newContent := append([]interface{}{dividerBlock()}, newIssueContent...)
	content := buildUpdateIssue(currentIssueContent, newContent)

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
		paragraphBlock(
			strongTextBlock("user: "),
			textBlock(contentInfos.UserName),
		),
		paragraphBlock(
			strongTextBlock("connection: "),
			textBlock(contentInfos.ConnectionName),
		),
	}

	descriptionContent = append(descriptionContent,
		paragraphBlock(
			strongTextBlock("script: "),
		),
		codeSnippetBlock(contentInfos.SessionScript),
	)

	descriptionContent = append(descriptionContent,
		paragraphBlock(
			strongTextBlock("session link: "),
			linkBlock(
				fmt.Sprintf("%v/sessions/%v", os.Getenv("API_URL"), contentInfos.SessionID),
				fmt.Sprintf("%v/sessions/%v", os.Getenv("API_URL"), contentInfos.SessionID),
			),
		),
	)

	return buildIssue(
		projectKey,
		summary,
		issueType,
		descriptionContent,
	)
}

type AddCreateReviewIssueTemplate struct {
	SessionID string
	ApiURL    string
}

func updateReviewJiraIssueTemplate(
	currentIssueContent []interface{},
	newContentInfos AddCreateReviewIssueTemplate,
) map[string]interface{} {
	descriptionContent := []interface{}{
		paragraphBlock(
			textBlock("This session requires review, access the following link to see it: \n"),
		),
		paragraphBlock(
			linkBlock(
				fmt.Sprintf("%v/sessions/%v", newContentInfos.ApiURL, newContentInfos.SessionID),
				fmt.Sprintf("%v/sessions/%v", newContentInfos.ApiURL, newContentInfos.SessionID),
			)),
	}

	return appendWithDivider(currentIssueContent, descriptionContent)
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
		paragraphBlock(
			textBlock(newContentInfos.ReviewerName),
			textBlock(" from "),
			strongTextBlock(newContentInfos.ReviewApprovedGroups),
			textBlock(" has "),
			strongTextBlock(newContentInfos.ReviewStatus),
			textBlock(" the session"),
		),
	}

	return appendWithDivider(currentIssueContent, descriptionContent)
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
		paragraphBlock(
			textBlock("The session is ready to be executed by the link:"),
		),
		paragraphBlock(
			linkBlock(
				fmt.Sprintf("%v/sessions/%v", newContentInfos.ApiURL, newContentInfos.SessionID),
				fmt.Sprintf("%v/sessions/%v", newContentInfos.ApiURL, newContentInfos.SessionID),
			)),
	}

	return appendWithDivider(currentIssueContent, descriptionContent)
}

func addReviewRejectedOrRevokedIssueTemplate(
	currentIssueContent []interface{},
	status string,
) map[string]interface{} {
	descriptionContent := []interface{}{
		paragraphBlock(
			textBlock(fmt.Sprintf("The session was %v", status)),
		),
	}

	return appendWithDivider(currentIssueContent, descriptionContent)
}

type AddSessionExecutedIssueTemplate struct {
	Payload string
}

func addSessionExecutedIssueTemplate(
	currentIssueContent []interface{},
	newContentInfo AddSessionExecutedIssueTemplate,
) map[string]interface{} {
	descriptionContent := []interface{}{
		paragraphBlock(
			textBlock("The session was executed with the response: \n"),
		),
		codeSnippetBlock(newContentInfo.Payload),
	}

	return appendWithDivider(currentIssueContent, descriptionContent)
}
