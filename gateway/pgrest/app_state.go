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
	//go:embed current_state.rollback
	currentStateRollback string
	//go:embed current_state.template.sql
	currentStateTemplateSQL string

	isStmtRe, _ = regexp.Compile(`[^function|view]`)
)

const (
	publicSchemeA string = "public"
	publicSchemeB string = "public_b"
)

type currentState struct {
	rollbackStmt    []string
	currentStateSQL string
	checksum        string
}

func getCurrentAppState(pgUser, roleName string) (*currentState, error) {
	if currentStateRollback == "" || currentStateTemplateSQL == "" {
		return nil, fmt.Errorf("app current state is empty, rollback_state=%v, current_state=%v",
			len(currentStateRollback), len(currentStateTemplateSQL))
	}
	tmpl, err := template.New("").Parse(currentStateTemplateSQL)
	if err != nil {
		return nil, fmt.Errorf("failed parsing current state template: %v", err)
	}
	// var data []byte
	data := bytes.NewBuffer([]byte{})
	inputs := map[string]any{"pgrest_role": roleName, "pg_app_user": pgUser, "target_schema": "public"}
	if err := tmpl.Execute(data, inputs); err != nil {
		return nil, fmt.Errorf("failed generating current state sql content: %v", err)
	}
	checksumData := sha256.Sum256([]byte(currentStateTemplateSQL))
	state := currentState{
		currentStateSQL: data.String(),
		checksum:        hex.EncodeToString(checksumData[:]),
	}
	state.rollbackStmt = parseRollbackStatements(currentStateRollback)
	if len(state.rollbackStmt) == 0 {
		return nil, fmt.Errorf("rollback statements are empty, content=%v", currentStateRollback)
	}
	return &state, nil
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

// func (s *AppStateRollout) HasFirst() bool  { return s.First != nil }
// func (s *AppStateRollout) HasSecond() bool { return s.Second != nil }
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

// func (s *AppStateRollout) RollbackStatemnt()

// func (s *AppStateRollout) GetRunningSchema() *AppState {
// 	if s.First != nil {
// 		return s.First
// 	}
// 	return s.Second
// }

func parseRollbackStatements(rollbackFileContent string) (dropStatements []string) {
	for _, line := range strings.Split(rollbackFileContent, "\n") {
		if !isStmtRe.MatchString(line) {
			continue
		}
		// TODO: should cascade?
		stmt := fmt.Sprintf("DROP %s", line)
		dropStatements = append(dropStatements, stmt)
	}
	return
}
