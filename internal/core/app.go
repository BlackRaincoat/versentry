package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/BlackRaincoat/versentry/internal/config"
	"github.com/BlackRaincoat/versentry/internal/health"
	"github.com/BlackRaincoat/versentry/internal/model"
	"github.com/BlackRaincoat/versentry/internal/netutil"
	"github.com/BlackRaincoat/versentry/internal/notifier"
	"github.com/BlackRaincoat/versentry/internal/provider"
	"github.com/BlackRaincoat/versentry/internal/registry"
	"github.com/BlackRaincoat/versentry/internal/state"
	"github.com/robfig/cron/v3"
)

// publicRegistryHosts are well-known public registries registered automatically
// as anonymous oci instances. Config entries for the same host override these.
//
// index.docker.io is Docker Hub (go-containerregistry / imageref normalize
// bare names like "nginx" to index.docker.io/library/nginx).
var publicRegistryHosts = []string{
	"index.docker.io",
	"ghcr.io",
	"quay.io",
	"registry.gitlab.com",
}

type notifierSlot struct {
	typ string
	n   notifier.Notifier
}

// App wires plugins and the engine from configuration.
type App struct {
	engine    *Engine
	notifiers []notifierSlot
	log       *slog.Logger
}

type passMode struct {
	suppress    bool
	updateState bool
}

type runCoordinator struct {
	mu      sync.Mutex
	running bool
	pending *passMode
	log     *slog.Logger
}

func (rc *runCoordinator) begin() {
	rc.mu.Lock()
	rc.running = true
	rc.mu.Unlock()
}

func (rc *runCoordinator) tryQueue(mode passMode) bool {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if !rc.running {
		return false
	}
	rc.queuePending(mode)
	return true
}

func (rc *runCoordinator) end() *passMode {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	next := rc.pending
	rc.pending = nil
	if next == nil {
		rc.running = false
	}
	return next
}

func (rc *runCoordinator) queuePending(mode passMode) {
	if rc.pending == nil {
		rc.pending = &mode
		return
	}
	// SIGUSR2 (full check) overrides a queued SIGUSR1 (scheduled).
	if !mode.suppress && !mode.updateState {
		rc.pending = &mode
	}
}

// NewApp builds all plugins and the engine from cfg.
func NewApp(cfg *config.Config, log *slog.Logger) (*App, error) {
	if log == nil {
		log = slog.Default()
	}

	prov, err := provider.New(cfg.Provider.Type, cfg.Provider.Config)
	if err != nil {
		return nil, fmt.Errorf("provider: %w", err)
	}

	regs, err := buildRegistries(cfg.Registries, cfg.RegistryProxy)
	if err != nil {
		return nil, err
	}
	if cfg.RegistryProxy != "" {
		log.Info("registry proxy enabled", "proxy", netutil.MaskProxyURL(cfg.RegistryProxy))
	}

	instanceName := config.ResolveInstanceName(cfg.InstanceName)
	log.Info("notification instance name", "instance", instanceName)

	notifiers := make([]notifierSlot, 0, len(cfg.Notifiers))
	for i, nc := range cfg.Notifiers {
		pluginCfg := copyPluginConfig(nc.Config)
		pluginCfg["instance_name"] = instanceName
		n, err := notifier.New(nc.Type, pluginCfg)
		if err != nil {
			return nil, fmt.Errorf("notifier[%d]: %w", i, err)
		}
		notifiers = append(notifiers, notifierSlot{typ: nc.Type, n: n})
	}

	configRules, err := NewConfigRuleResolver(cfg.Rules, log)
	if err != nil {
		return nil, fmt.Errorf("rules: %w", err)
	}
	// Config wins over container labels (variant A).
	rules := NewChainRuleResolver(configRules, NewLabelRuleResolver(log))

	engine := NewEngine(prov, regs, cfg.Timeouts, log, rules)

	return &App{
		engine:    engine,
		notifiers: notifiers,
		log:       log,
	}, nil
}

func copyPluginConfig(cfg map[string]any) map[string]any {
	out := make(map[string]any, len(cfg)+1)
	for k, v := range cfg {
		out[k] = v
	}
	return out
}

func (a *App) runHealthHeartbeat(ctx context.Context, configPath string, cfg *config.Config) {
	interval := health.HeartbeatInterval(cfg)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := health.Touch(configPath, cfg.StateFile); err != nil {
				a.log.Warn("health heartbeat failed", "error", err)
			}
		}
	}
}

// buildRegistries creates configured oci registries, then fills in anonymous
// public hosts that are not already covered by config (config wins on same host).
// globalProxy applies to every oci instance unless the entry sets its own proxy.
func buildRegistries(cfgs []config.PluginConfig, globalProxy string) ([]registry.Registry, error) {
	type entry struct {
		typ string
		idx int
		reg registry.Registry
	}

	entries := make([]entry, 0, len(cfgs)+len(publicRegistryHosts))
	seen := make(map[string]entry, len(cfgs)+len(publicRegistryHosts))

	for i, rc := range cfgs {
		reg, err := registry.New(rc.Type, withRegistryProxy(rc.Config, globalProxy))
		if err != nil {
			return nil, fmt.Errorf("registry[%d]: %w", i, err)
		}
		host := reg.Host()
		if prev, ok := seen[host]; ok {
			return nil, fmt.Errorf(
				"registry[%d] (%s) host %q conflicts with registry[%d] (%s)",
				i, rc.Type, host, prev.idx, prev.typ,
			)
		}
		e := entry{typ: rc.Type, idx: i, reg: reg}
		entries = append(entries, e)
		seen[host] = e
	}

	for _, host := range publicRegistryHosts {
		if _, ok := seen[host]; ok {
			continue
		}
		reg, err := registry.New("oci", withRegistryProxy(map[string]any{"host": host}, globalProxy))
		if err != nil {
			return nil, fmt.Errorf("builtin public registry %q: %w", host, err)
		}
		e := entry{typ: "oci", idx: -1, reg: reg}
		entries = append(entries, e)
		seen[host] = e
	}

	regs := make([]registry.Registry, 0, len(entries))
	for _, e := range entries {
		if e.idx >= 0 {
			regs = append(regs, e.reg)
		}
	}
	for _, e := range entries {
		if e.idx < 0 {
			regs = append(regs, e.reg)
		}
	}

	return regs, nil
}

func withRegistryProxy(cfg map[string]any, globalProxy string) map[string]any {
	if globalProxy == "" {
		return cfg
	}
	out := make(map[string]any, len(cfg)+1)
	for k, v := range cfg {
		out[k] = v
	}
	if _, ok := out["proxy"]; !ok {
		out["proxy"] = globalProxy
	}
	return out
}

func (a *App) notifyAll(ctx context.Context, batch []model.UpdateAvailable) bool {
	allOK := true
	for _, slot := range a.notifiers {
		if err := slot.n.Notify(ctx, batch); err != nil {
			a.log.Error("notifier failed", "notifier", slot.typ, "error", err)
			allOK = false
			continue
		}
		a.log.Info("notifier delivered", "notifier", slot.typ, "updates", len(batch))
	}
	return allOK
}

// runPass runs one detection pass and optionally filters/saves notification state.
// suppress reads state to skip already-notified targets; updateState writes state
// after successful delivery. They are independent (SIGUSR2 sets both false).
func (a *App) runPass(ctx context.Context, store *state.Store, mode passMode) error {
	updates, activeKeys, canPrune, err := a.engine.RunOnce(ctx)
	if err != nil {
		return err
	}

	toNotify := updates
	suppressed := 0
	if mode.suppress {
		if store == nil {
			return fmt.Errorf("suppress requested without state store")
		}
		var suppressedBatch []model.UpdateAvailable
		toNotify, suppressedBatch = store.Filter(updates)
		suppressed = len(suppressedBatch)
	}

	notified := toNotify
	if len(toNotify) > 0 {
		if !a.notifyAll(ctx, toNotify) {
			a.log.Info("notifications not delivered, state not updated", "updates", len(toNotify))
			notified = nil
		}
	} else if suppressed > 0 {
		a.log.Info("notifications suppressed", "suppressed", suppressed)
	}

	if mode.updateState {
		if store == nil {
			return fmt.Errorf("updateState requested without state store")
		}
		if err := store.AfterPass(activeKeys, notified, canPrune); err != nil {
			return fmt.Errorf("save state: %w", err)
		}
	}

	return nil
}

// Check runs a single stateless pass (no state read/write) and exits.
func (a *App) Check(ctx context.Context) error {
	return a.runPass(ctx, nil, passMode{suppress: false, updateState: false})
}

// Run performs periodic checks until ctx is cancelled.
func (a *App) Run(ctx context.Context, cfg *config.Config, configPath, statePath string) error {
	a.log.Info("starting periodic checks",
		"interval", cfg.Interval.Duration,
		"schedule", cfg.Schedule,
		"state_file", statePath,
	)

	store := state.Load(statePath, a.log)
	a.log.Info("notification state loaded", "path", statePath, "entries", store.EntryCount())

	stampPath := health.ResolveStampPath(configPath, cfg.StateFile)
	if err := health.Touch(configPath, cfg.StateFile); err != nil {
		return fmt.Errorf("health stamp %s: %w", stampPath, err)
	}
	a.log.Info("health stamp updated", "path", stampPath)

	heartbeatCtx, stopHeartbeat := context.WithCancel(ctx)
	defer stopHeartbeat()
	go a.runHealthHeartbeat(heartbeatCtx, configPath, cfg)

	scheduleCh, stopSchedule, resetSchedule, err := newScheduleSource(cfg, a.log)
	if err != nil {
		return err
	}
	defer stopSchedule()

	sigUSR1 := make(chan os.Signal, 1)
	sigUSR2 := make(chan os.Signal, 1)
	signal.Notify(sigUSR1, syscall.SIGUSR1)
	signal.Notify(sigUSR2, syscall.SIGUSR2)
	defer signal.Stop(sigUSR1)
	defer signal.Stop(sigUSR2)

	coord := &runCoordinator{log: a.log}
	scheduled := passMode{suppress: true, updateState: true}
	forceCheck := passMode{suppress: false, updateState: false}

	storeFor := func(mode passMode) *state.Store {
		if mode.suppress || mode.updateState {
			return store
		}
		return nil
	}

	runChecks := func(mode passMode) error {
		for {
			if err := a.runPass(ctx, storeFor(mode), mode); err != nil {
				coord.mu.Lock()
				coord.running = false
				coord.pending = nil
				coord.mu.Unlock()
				return err
			}
			if err := health.Touch(configPath, cfg.StateFile); err != nil {
				a.log.Warn("health stamp update failed", "error", err)
			}
			next := coord.end()
			if next == nil {
				return nil
			}
			a.log.Info("running queued check",
				"suppress", next.suppress,
				"update_state", next.updateState,
			)
			mode = *next
		}
	}

	trigger := func(mode passMode, label string, resetAfter bool) error {
		if coord.tryQueue(mode) {
			coord.log.Debug("check already running, signal queued", "signal", label)
			return nil
		}
		coord.begin()
		a.log.Info(label)
		if err := runChecks(mode); err != nil {
			return err
		}
		if resetAfter {
			resetSchedule()
		}
		return nil
	}

	if state.MissingFile(statePath) {
		if err := trigger(scheduled, "initial check (no state file)", true); err != nil {
			return a.finishRun(ctx, err)
		}
	}

	for {
		select {
		case <-ctx.Done():
			return a.finishRun(ctx, ctx.Err())
		case <-scheduleCh:
			if err := trigger(scheduled, "scheduled check", true); err != nil {
				return a.finishRun(ctx, err)
			}
		case <-sigUSR1:
			if err := trigger(scheduled, "forced scheduled check (SIGUSR1)", true); err != nil {
				return a.finishRun(ctx, err)
			}
		case <-sigUSR2:
			if err := trigger(forceCheck, "forced full check (SIGUSR2)", false); err != nil {
				return a.finishRun(ctx, err)
			}
		}
	}
}

// finishRun treats context.Canceled as a clean exit when ctx itself was canceled
// (SIGINT/SIGTERM via signal.NotifyContext). Other errors pass through unchanged.
func (a *App) finishRun(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) && errors.Is(ctx.Err(), context.Canceled) {
		a.log.Info("shutting down (signal received)")
		return nil
	}
	return err
}

func newScheduleSource(cfg *config.Config, log *slog.Logger) (<-chan time.Time, func(), func(), error) {
	if cfg.Schedule != "" {
		loc, err := cfg.ScheduleLocation()
		if err != nil {
			return nil, nil, nil, err
		}
		ch := make(chan time.Time, 1)
		sched := cron.New(cron.WithLocation(loc))
		_, err = sched.AddFunc(cfg.Schedule, func() {
			select {
			case ch <- time.Now():
			default:
			}
		})
		if err != nil {
			return nil, nil, nil, fmt.Errorf("schedule %q: %w", cfg.Schedule, err)
		}
		sched.Start()
		stop := func() { sched.Stop() }
		reset := func() {} // cron fires on wall-clock schedule
		log.Info("using cron schedule", "schedule", cfg.Schedule, "timezone", loc.String())
		return ch, stop, reset, nil
	}

	interval := cfg.Interval.Duration
	ticker := time.NewTicker(interval)
	stop := func() { ticker.Stop() }
	reset := func() { ticker.Reset(interval) }
	log.Info("using interval schedule", "interval", interval)
	return ticker.C, stop, reset, nil
}
