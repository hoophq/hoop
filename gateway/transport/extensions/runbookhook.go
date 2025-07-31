package transportext

import (
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/memory"
	"github.com/hoophq/hoop/common/proto"
	pbsystem "github.com/hoophq/hoop/common/proto/system"
	"github.com/hoophq/hoop/common/runbooks"
	"github.com/hoophq/hoop/gateway/models"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
	transportsystem "github.com/hoophq/hoop/gateway/transport/system"
)

var hookStore = memory.New()

const (
	sessionOpenHookFileName  string = "hoop-hooks/session-open.runbook.py"
	sessionCloseHookFileName string = "hoop-hooks/session-close.runbook.py"
	hookDefaultCommand       string = "python3"
)

type runbookWrapperFiles struct {
	sessionOpen   *runbooks.File
	sessionClose  *runbooks.File
	cacheDuration time.Duration
	expireAt      time.Time
}

func getRunbookHookFiles(ctx Context) (*runbookWrapperFiles, error) {
	p, err := models.GetPluginByName(ctx.OrgID, plugintypes.PluginRunbooksName)
	switch err {
	case models.ErrNotFound:
		return nil, nil
	case nil:
	default:
		return nil, err
	}

	config, err := runbooks.NewConfig(p.EnvVars)
	switch err {
	case runbooks.ErrEmptyConfiguration:
		return nil, nil
	case nil:
		if config.HookCacheTTL == nil {
			return nil, nil
		}
	default:
		return nil, err
	}

	// cache validation
	if f, ok := hookStore.Get(ctx.OrgID).(*runbookWrapperFiles); ok {
		now := time.Now().UTC()
		isExpired := now.After(f.expireAt)
		hasConfigTTLChanged := f.cacheDuration != *config.HookCacheTTL

		// clear cache if it's expired or the ttl configuration has changed
		log.With("sid", ctx.SID).Infof("loading runbook hook from cache, cache-ttl=%v, cachehit=%v, config-ttl-changed=%v",
			config.HookCacheTTL.String(), !isExpired, hasConfigTTLChanged)
		if !isExpired && !hasConfigTTLChanged {
			return f, nil
		}

		// remove the cache and fetch the file again
		hookStore.Del(ctx.OrgID)
	}

	repository, err := runbooks.FetchRepository(config)
	if err != nil {
		return nil, err
	}

	sessionOpenF, err := repository.ReadFile(sessionOpenHookFileName, map[string]string{})
	if err != nil {
		return nil, err
	}
	sessionCloseF, err := repository.ReadFile(sessionCloseHookFileName, map[string]string{})
	if err != nil {
		return nil, err
	}
	f := &runbookWrapperFiles{
		sessionOpen:   sessionOpenF,
		sessionClose:  sessionCloseF,
		expireAt:      time.Now().UTC().Add(*config.HookCacheTTL),
		cacheDuration: *config.HookCacheTTL,
	}
	hookStore.Set(ctx.OrgID, f)
	return f, nil
}

func processEventOpenSessionHook(ctx Context, pkt *proto.Packet) {
	hook, err := getRunbookHookFiles(ctx)
	if err != nil {
		log.With("sid", ctx.SID).Warnf("session-open: failed fetching runbook hook, reason=%v", err)
		return
	}
	if hook == nil || hook.sessionOpen == nil {
		return
	}

	log.With("sid", ctx.SID).Infof("session-open: runbook hook started, commit=%v, filesize=%v",
		hook.sessionOpen.CommitSHA, len(hook.sessionOpen.InputFile))

	go func() {
		requestID := uuid.NewSHA1(uuid.NameSpaceURL, fmt.Appendf(nil, "session-open/%s", ctx.SID)).String()
		resp := transportsystem.RunRunbookHook(ctx.AgentID, &pbsystem.RunbookHookRequest{
			ID:        requestID,
			SID:       ctx.SID,
			Command:   []string{hookDefaultCommand},
			InputFile: string(hook.sessionOpen.InputFile),
			EventSessionOpen: &pbsystem.EventSessionOpen{
				Verb:                ctx.Verb,
				ConnectionName:      ctx.ConnectionName,
				ConnectionType:      ctx.ConnectionType,
				ConnectionSubType:   ctx.ConnectionSubType,
				ConnectionEnvs:      ctx.ConnectionEnvs,
				ConnectionReviewers: ctx.ConnectionReviewers,
				Input:               string(pkt.Payload),
				UserEmail:           ctx.UserEmail,
			},
		})
		log.With("sid", ctx.SID).Infof("session-open: runbook hook finished, duration=%v, exit-code=%v, output=%v",
			resp.ExecutionTimeSec, resp.ExitCode, resp.Output)
	}()
}

func processEventCloseSessiontHook(ctx Context, pkt *proto.Packet) {
	hook, err := getRunbookHookFiles(ctx)
	if err != nil {
		log.With("sid", ctx.SID).Warnf("session-close: failed fetching runbook hook, reason=%v", err)
		return
	}
	if hook == nil || hook.sessionClose == nil {
		return
	}

	exitCode := -2
	exitCodeInt, err := strconv.Atoi(string(pkt.Spec[proto.SpecClientExitCodeKey]))
	if err == nil {
		exitCode = exitCodeInt
	}
	var outputErr *string
	if len(pkt.Payload) > 0 {
		outputErr = func() *string { v := string(pkt.Payload); return &v }()
	}

	log.With("sid", ctx.SID).Infof("session-close: runbook hook started, commit=%v, filesize=%v",
		hook.sessionOpen.CommitSHA, len(hook.sessionClose.InputFile))

	go func() {
		requestID := uuid.NewSHA1(uuid.NameSpaceURL, fmt.Appendf(nil, "session-close/%s", ctx.SID)).String()
		resp := transportsystem.RunRunbookHook(ctx.AgentID, &pbsystem.RunbookHookRequest{
			ID:        requestID,
			SID:       ctx.SID,
			Command:   []string{hookDefaultCommand},
			InputFile: string(hook.sessionClose.InputFile),
			EventSessionClose: &pbsystem.EventSessionClose{
				ExitCode: exitCode,
				Output:   outputErr,
			},
		})
		log.With("sid", ctx.SID).Infof("session-close: runbook hook finished, duration=%v, exit-code=%v, output=%v",
			resp.ExecutionTimeSec, resp.ExitCode, resp.Output)
	}()

}
