package pgrest

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"text/template"
	"time"
)

var (
	//go:embed current_state.template.rollback
	currentStateRollback string
	//go:embed current_state.template.sql
	currentStateTemplateSQL string

	isStmtRe, _ = regexp.Compile(`^[function|view]`)
)

const (
	publicSchemeA string = "public"
	publicSchemeB string = "public_b"
)

func renderTmpl(content string, inputs map[string]any) (string, error) {
	tmpl, err := template.New("").Parse(content)
	if err != nil {
		return "", err
	}
	data := bytes.NewBuffer([]byte{})
	// inputs := map[string]any{"pgrest_role": roleName, "pg_app_user": pgUser, "target_schema": "public"}
	if err := tmpl.Execute(data, inputs); err != nil {
		return "", err
	}
	return data.String(), nil
}

func parseAppStateSQL(roleName, pgUser, targetSchema string) (string, error) {
	inputs := map[string]any{"pgrest_role": roleName, "pg_app_user": pgUser, "target_schema": targetSchema}
	appStmt, err := renderTmpl(currentStateTemplateSQL, inputs)
	if err != nil {
		return "", fmt.Errorf("failed rendering app state: %v", err)
	}
	return appStmt, nil
}

func getCurrentAppStateChecksum() string {
	checksumData := sha256.Sum256([]byte(currentStateTemplateSQL))
	return hex.EncodeToString(checksumData[:])
}

func validateAppState() (err error) {
	if len(currentStateRollback) == 0 || len(currentStateTemplateSQL) == 0 {
		err = fmt.Errorf("current app state are empty, state-file-length=%v, state-rollback-file-length=%v",
			len(currentStateTemplateSQL), len(currentStateRollback))
	}
	return
}

type AppState struct {
	ID            int
	StateRollback string
	Checksum      string
	Schema        string
	RoleName      string
	Version       string
	PgVersion     string
	GitCommit     string
	CreatedAt     time.Time
}

type AppStateRollout struct {
	First  *AppState
	Second *AppState
}

func (s *AppStateRollout) ShouldRollout(checksum string) bool {
	if s.First == nil || s.Second == nil {
		return true
	}
	return checksum == s.First.Checksum || checksum == s.Second.Checksum
}

func (s *AppStateRollout) GetAppState(checksum string) (bool, *AppState) {
	if s.First != nil && s.First.Checksum == checksum {
		return true, s.First
	}
	if s.Second != nil && s.Second.Checksum == checksum {
		return true, s.Second
	}
	if s.First != nil {
		return false, s.First
	}
	if s.Second != nil {
		return false, s.Second
	}
	return false, nil
}

func parseRollbackStatements(rollbackFileContent, targetSchema string) []string {
	var dropStatements []string
	for _, line := range strings.Split(rollbackFileContent, "\n") {
		if !isStmtRe.MatchString(line) {
			continue
		}
		// TODO: should cascade?
		stmt := fmt.Sprintf("DROP %s", line)
		dropStatements = append(dropStatements, stmt)
	}
	if len(dropStatements) > 0 {
		dropStatements = append([]string{"SET search_path TO " + targetSchema}, dropStatements...)
	}
	return dropStatements
}
