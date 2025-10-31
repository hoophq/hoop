package transportext

import (
	"fmt"
	"slices"
	"strconv"
	"sync"
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
var hookStoreV2 = sync.Map{} // map[orgID][]*runbookWrapperFiles

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

func getRunbookHookFilesV2(ctx Context) ([]*runbookWrapperFiles, error) {
	var hooks []*runbookWrapperFiles
	runbooksConfig, err := models.GetRunbookConfigurationByOrgID(models.DB, ctx.OrgID)
	if err != nil {
		if err == models.ErrNotFound {
			return hooks, nil
		}
		return nil, err
	}

	if cached, ok := hookStoreV2.Load(ctx.OrgID); ok {
		cachedHooks := cached.([]*runbookWrapperFiles)
		now := time.Now().UTC()

		hasExpired := slices.ContainsFunc(cachedHooks, func(hook *runbookWrapperFiles) bool {
			return now.After(hook.expireAt)
		})
		if !hasExpired {
			log.With("sid", ctx.SID).Infof("loading runbook hook V2 from cache")
			return cachedHooks, nil
		}

		hookStoreV2.Delete(ctx.OrgID)
	}

	// Fetch hooks from repositories
	for _, repositoryConfig := range runbooksConfig.RepositoryConfigs {
		config, err := runbooks.NewConfig(repositoryConfig)
		if err != nil {
			return nil, err
		}

		if config.HookCacheTTL == nil {
			return hooks, nil
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

		hooks = append(hooks, f)
	}
	hookStoreV2.Store(ctx.OrgID, hooks)

	return hooks, nil
}

func executeOpenSessionRunbookHook(ctx Context, pkt *proto.Packet, hook *runbookWrapperFiles) {
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
}

func processEventOpenSessionHook(ctx Context, pkt *proto.Packet) {
	hook, err := getRunbookHookFiles(ctx)
	if err != nil {
		log.With("sid", ctx.SID).Warnf("session-open: failed fetching runbook hook, reason=%v", err)
		return
	}
	hooksV2, err := getRunbookHookFilesV2(ctx)
	if err != nil {
		log.With("sid", ctx.SID).Warnf("session-open: failed fetching runbook hookV2, reason=%v", err)
		return
	}

	// hook execution
	if hook != nil && hook.sessionOpen != nil {
		log.With("sid", ctx.SID).Infof("session-open: runbook hook started, commit=%v, filesize=%v",
			hook.sessionOpen.CommitSHA, len(hook.sessionOpen.InputFile))

		go func() { executeOpenSessionRunbookHook(ctx, pkt, hook) }()
	}

	// hooks v2 execution
	for _, hookV2 := range hooksV2 {
		if hookV2.sessionOpen == nil {
			continue
		}

		log.With("sid", ctx.SID).Infof("session-open: runbook hook v2 started, commit=%v, filesize=%v",
			hookV2.sessionOpen.CommitSHA, len(hookV2.sessionOpen.InputFile))

		go func() { executeOpenSessionRunbookHook(ctx, pkt, hookV2) }()
	}
}

func executeCloseSessionRunbookHook(ctx Context, hook *runbookWrapperFiles, exitCode int, outputErr *string) {
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
}

func processEventCloseSessiontHook(ctx Context, pkt *proto.Packet) {
	hook, err := getRunbookHookFiles(ctx)
	if err != nil {
		log.With("sid", ctx.SID).Warnf("session-close: failed fetching runbook hook, reason=%v", err)
		return
	}
	hooksV2, err := getRunbookHookFilesV2(ctx)
	if err != nil {
		log.With("sid", ctx.SID).Warnf("session-close: failed fetching runbook hookV2, reason=%v", err)
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

	if hook != nil && hook.sessionClose != nil {
		log.With("sid", ctx.SID).Infof("session-close: runbook hook started, commit=%v, filesize=%v",
			hook.sessionOpen.CommitSHA, len(hook.sessionClose.InputFile))

		go func() { executeCloseSessionRunbookHook(ctx, hook, exitCode, outputErr) }()
	}

	for _, hookV2 := range hooksV2 {
		if hookV2.sessionClose == nil {
			continue
		}

		log.With("sid", ctx.SID).Infof("session-close: runbook hook V2 started, commit=%v, filesize=%v",
			hookV2.sessionOpen.CommitSHA, len(hookV2.sessionClose.InputFile))

		go func() { executeCloseSessionRunbookHook(ctx, hookV2, exitCode, outputErr) }()
	}
}
