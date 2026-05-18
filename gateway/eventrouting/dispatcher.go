package eventrouting

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	commonRunbooks "github.com/hoophq/hoop/common/runbooks"
	"github.com/hoophq/hoop/gateway/clientexec"
	"github.com/hoophq/hoop/gateway/events"
	"github.com/hoophq/hoop/gateway/models"
)

const dispatchExecTimeout = 60 * time.Second

func processDispatch(pCtx context.Context, d *models.EventDispatch) error {
	sub, event, err := models.GetDispatchContext(models.DB, d.ID)
	if err != nil {
		return fmt.Errorf("failed loading dispatch context: %w", err)
	}

	if sub.Status != "active" {
		return fmt.Errorf("subscription %q is %s, skipping", sub.ID, sub.Status)
	}

	params, err := events.RenderParameters(sub.ParameterMapping, event.Payload)
	if err != nil {
		return fmt.Errorf("failed rendering parameters: %w", err)
	}

	conn, err := models.GetConnectionByNameOrID(models.NewAdminContext(sub.OrgID), sub.ConnectionID)
	if err != nil || conn == nil {
		return fmt.Errorf("failed loading connection %q: %v", sub.ConnectionID, err)
	}

	if !conn.AgentID.Valid || conn.AgentID.String == "" {
		return fmt.Errorf("connection %q has no agent assigned", conn.Name)
	}

	rbConfig, err := models.GetRunbookConfigurationByOrgID(models.DB, sub.OrgID)
	if err != nil {
		return fmt.Errorf("failed loading runbook configuration: %w", err)
	}

	repoKey := sub.RunbookRepository
	repoConf, ok := rbConfig.RepositoryConfigs[repoKey]
	if !ok {
		return fmt.Errorf("runbook repository %q not found in org configuration", repoKey)
	}

	gitConf, err := models.BuildCommonConfig(&repoConf)
	if err != nil {
		return fmt.Errorf("failed building git config for %q: %w", repoKey, err)
	}

	repo, err := commonRunbooks.FetchRepository(gitConf)
	if err != nil {
		return fmt.Errorf("failed fetching repository %q: %w", repoKey, err)
	}

	file, err := repo.ReadFile(sub.RunbookFile, params)
	if err != nil {
		return fmt.Errorf("failed reading runbook file %q: %w", sub.RunbookFile, err)
	}
	if file == nil {
		return fmt.Errorf("runbook file %q not found in repository %q", sub.RunbookFile, repoKey)
	}

	sessionID := uuid.NewString()
	client, err := clientexec.New(&clientexec.Options{
		OrgID:                  sub.OrgID,
		SessionID:              sessionID,
		ConnectionName:         conn.Name,
		UserAgent:              "eventrouting.dispatcher",
		Origin:                 pb.ConnectionOriginClientAPI,
		Verb:                   pb.ClientVerbExec,
		ImpersonateUserSubject: sub.CreatedByUserID,
	})
	if err != nil {
		return fmt.Errorf("failed creating exec client: %w", err)
	}
	defer client.Close()

	hookID := uuid.NewString()
	_ = models.SetDispatchSessionID(models.DB, d.ID, hookID)

	log.With(
		"dispatch_id", d.ID,
		"sid", sessionID,
		"connection", conn.Name,
		"runbook", sub.RunbookFile,
		"agent_id", conn.AgentID.String,
		"impersonate", sub.CreatedByUserID,
	).Infof("event-routing: executing runbook via connection %q", conn.Name)

	respCh := make(chan *clientexec.Response, 1)
	go func() {
		respCh <- client.Run(file.InputFile, nil)
	}()

	timeoutCtx, cancel := context.WithTimeout(pCtx, dispatchExecTimeout)
	defer cancel()

	select {
	case resp := <-respCh:
		if resp.HasReview {
			return fmt.Errorf("dispatch blocked by review (review_uri=%s)", resp.Output)
		}
		if resp.ExitCode != 0 {
			return fmt.Errorf("runbook exited with code %d: %s", resp.ExitCode, truncate(resp.Output, 500))
		}
		log.With("dispatch_id", d.ID, "sid", sessionID).
			Infof("event-routing: runbook finished, exit=%d, truncated=%v, duration_ms=%d",
				resp.ExitCode, resp.Truncated, resp.ExecutionTimeMili)
		return nil
	case <-timeoutCtx.Done():
		client.Close()
		return fmt.Errorf("dispatch exec timed out after %s", dispatchExecTimeout)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
