package loop

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/enes-alatas/spool/internal/bus"
	"github.com/enes-alatas/spool/internal/runtime"
	"github.com/enes-alatas/spool/internal/store"
)

// FamilyAliases are the model aliases every dropdown offers, in the order it
// offers them (#329). The CLI resolves each to its family's latest model.
var FamilyAliases = []string{"fable", "opus", "sonnet", "haiku"}

// resolveTimeout bounds one resolution run: the CLI reports init within a
// couple of seconds on the host, and the rest covers a cold container start
// (ADR-0033).
const resolveTimeout = 30 * time.Second

// ErrInvalidModel is what Models.Add returns for a name that cannot be a
// model id: empty, too long, with whitespace, or shaped like a flag.
var ErrInvalidModel = errors.New("invalid model")

// maxModelLen and maxLabelLen bound what an operator can put on the list.
const (
	maxModelLen = 200
	maxLabelLen = 100
)

// Models is the hub's model list: the family aliases and the operator's
// custom entries, each with what it runs as on this hub (ADR-0033, #332).
// Resolutions come from a run of the default runtime's claude that can reach
// nothing, and are refreshed by what real turns on that runtime report.
type Models struct {
	store      store.Store
	bus        *bus.Bus
	runtime    runtime.Runtime // the default runtime's, which new loops get
	kind       string          // its kind, to tell which turns ran on it
	cliVersion string          // what it runs, stored with each probe
	log        *slog.Logger
	startedAt  int64 // a turn observed since then outranks this run's probes

	mu       sync.Mutex
	inflight map[string]bool
}

// NewModels returns the model list for a hub whose default runtime is rt, of
// kind kind, running claude cliVersion.
func NewModels(st store.Store, publisher *bus.Bus, rt runtime.Runtime, kind, cliVersion string, log *slog.Logger) *Models {
	return &Models{store: st, bus: publisher, runtime: rt, kind: kind, cliVersion: cliVersion, log: log,
		startedAt: time.Now().UnixMilli(), inflight: map[string]bool{}}
}

// ModelsView is the whole list as the control room reads it.
type ModelsView struct {
	Aliases []store.ModelResolution `json:"aliases"`
	Custom  []CustomModelView       `json:"custom"`
}

// CustomModelView is a custom entry with its resolution.
type CustomModelView struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	store.ModelResolution
}

// Start resolves every name on the list in the background: the family
// aliases, and the custom entries, so one whose run failed gets another
// chance. Nothing waits on it.
func (models *Models) Start(ctx context.Context) {
	names := append([]string{}, FamilyAliases...)
	custom, err := models.store.Models().ListCustom(ctx)
	if err != nil {
		models.log.Error("list custom models", "err", err)
	}
	for _, entry := range custom {
		names = append(names, entry.Model)
	}
	for _, name := range names {
		models.resolve(ctx, name)
	}
}

// resolve starts a run for name unless one is already going. The result is
// stored and published; a failure is logged and leaves the name showing bare.
func (models *Models) resolve(ctx context.Context, name string) {
	models.mu.Lock()
	if models.inflight[name] {
		models.mu.Unlock()
		return
	}
	models.inflight[name] = true
	models.mu.Unlock()

	go func() {
		defer func() {
			models.mu.Lock()
			delete(models.inflight, name)
			models.mu.Unlock()
		}()
		runCtx, cancel := context.WithTimeout(ctx, resolveTimeout)
		defer cancel()
		resolved, err := models.runtime.ResolveModel(runCtx, name)
		if err != nil {
			if ctx.Err() == nil {
				models.log.Warn("model not resolved; it shows bare until the next start", "model", name, "err", err)
			}
			return
		}
		current, err := models.resolutions(ctx)
		if err != nil {
			models.log.Error("read model resolutions", "err", err)
			return
		}
		if res, ok := current[name]; ok && res.Source == store.ResolutionTurn && res.ResolvedAt >= models.startedAt {
			return // a real turn has already said, under the operator's login
		}
		models.set(ctx, &store.ModelResolution{Model: name, Resolved: resolved,
			Source: store.ResolutionProbe, CLIVersion: models.cliVersion, ResolvedAt: time.Now().UnixMilli()})
	}()
}

// Observe takes what a turn's init reported as resolved for the model l's
// process was spawned on. Only a loop on the default runtime and image
// counts, since that is the claude the probe asks (ADR-0033 item 4): a loop
// on an image of its own says what it ran on through its own resolved_model
// and leaves the list alone.
func (models *Models) Observe(loopRecord *store.Loop, spawned, resolved string) {
	if resolved == "" || spawned == "" || loopRecord.Runtime != models.kind || loopRecord.Image != "" {
		return
	}
	ctx := context.Background()
	if !models.listed(ctx, spawned) {
		return
	}
	current, err := models.resolutions(ctx)
	if err != nil {
		models.log.Error("read model resolutions", "err", err)
		return
	}
	if res, ok := current[spawned]; ok && res.Resolved == resolved && res.Source == store.ResolutionTurn {
		return
	}
	models.set(ctx, &store.ModelResolution{Model: spawned, Resolved: resolved,
		Source: store.ResolutionTurn, ResolvedAt: time.Now().UnixMilli()})
}

// listed reports whether name is on the list: a family alias or a custom
// entry. A loop on a model typed in once is not.
func (models *Models) listed(ctx context.Context, name string) bool {
	for _, alias := range FamilyAliases {
		if name == alias {
			return true
		}
	}
	custom, err := models.store.Models().ListCustom(ctx)
	if err != nil {
		return false
	}
	for _, entry := range custom {
		if entry.Model == name {
			return true
		}
	}
	return false
}

func (models *Models) set(ctx context.Context, res *store.ModelResolution) {
	if err := models.store.Models().SetResolution(ctx, res); err != nil {
		models.log.Error("store model resolution", "model", res.Model, "err", err)
		return
	}
	models.publish(ctx)
}

func (models *Models) resolutions(ctx context.Context) (map[string]store.ModelResolution, error) {
	rows, err := models.store.Models().Resolutions(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]store.ModelResolution, len(rows))
	for _, row := range rows {
		out[row.Model] = *row
	}
	return out, nil
}

// View returns the list with each name's resolution. A name nothing has
// resolved carries only itself.
func (models *Models) View(ctx context.Context) (ModelsView, error) {
	resolved, err := models.resolutions(ctx)
	if err != nil {
		return ModelsView{}, err
	}
	custom, err := models.store.Models().ListCustom(ctx)
	if err != nil {
		return ModelsView{}, err
	}
	view := ModelsView{Aliases: []store.ModelResolution{}, Custom: []CustomModelView{}}
	for _, alias := range FamilyAliases {
		view.Aliases = append(view.Aliases, resolutionOf(resolved, alias))
	}
	for _, entry := range custom {
		view.Custom = append(view.Custom, CustomModelView{ID: entry.ID, Label: entry.Label,
			ModelResolution: resolutionOf(resolved, entry.Model)})
	}
	return view, nil
}

func resolutionOf(resolved map[string]store.ModelResolution, name string) store.ModelResolution {
	if res, ok := resolved[name]; ok {
		return res
	}
	return store.ModelResolution{Model: name}
}

// publish sends the whole list to the control room, which renders from it.
func (models *Models) publish(ctx context.Context) {
	view, err := models.View(ctx)
	if err != nil {
		models.log.Error("read model list", "err", err)
		return
	}
	models.bus.Publish(bus.Item{Kind: bus.KindModels, Payload: view})
}

// Add puts a custom entry on the list and starts resolving it. The model is
// an id as the CLI's --model takes it; ErrInvalidModel if it cannot be one,
// store.ErrDuplicate if it is already listed, aliases included.
func (models *Models) Add(ctx context.Context, model, label string) (*CustomModelView, error) {
	model, label = strings.TrimSpace(model), strings.TrimSpace(label)
	if err := validModel(model); err != nil {
		return nil, err
	}
	if len(label) > maxLabelLen {
		return nil, fmt.Errorf("%w: a label is at most %d characters", ErrInvalidModel, maxLabelLen)
	}
	for _, alias := range FamilyAliases {
		if model == alias {
			return nil, store.ErrDuplicate
		}
	}
	entry := &store.CustomModel{ID: "cm_" + newUUID(), Model: model, Label: label, CreatedAt: time.Now().UnixMilli()}
	if err := models.store.Models().AddCustom(ctx, entry); err != nil {
		return nil, err
	}
	models.publish(ctx)
	models.resolve(context.WithoutCancel(ctx), model)
	return &CustomModelView{ID: entry.ID, Label: entry.Label, ModelResolution: store.ModelResolution{Model: model}}, nil
}

// Relabel changes a custom entry's label.
func (models *Models) Relabel(ctx context.Context, id, label string) (*CustomModelView, error) {
	label = strings.TrimSpace(label)
	if len(label) > maxLabelLen {
		return nil, fmt.Errorf("%w: a label is at most %d characters", ErrInvalidModel, maxLabelLen)
	}
	entry, err := models.store.Models().SetCustomLabel(ctx, id, label)
	if err != nil {
		return nil, err
	}
	resolved, err := models.resolutions(ctx)
	if err != nil {
		return nil, err
	}
	models.publish(ctx)
	return &CustomModelView{ID: entry.ID, Label: entry.Label, ModelResolution: resolutionOf(resolved, entry.Model)}, nil
}

// Delete takes a custom entry off the list. Loops already on that model keep
// it: the list only feeds the dropdowns.
func (models *Models) Delete(ctx context.Context, id string) error {
	if err := models.store.Models().DeleteCustom(ctx, id); err != nil {
		return err
	}
	models.publish(ctx)
	return nil
}

// ValidLoopModel is validModel for a loop's own model, where empty is not
// missing but the CLI's default (#341).
func ValidLoopModel(model string) error {
	if model == "" {
		return nil
	}
	return validModel(model)
}

// validModel rejects what cannot be a model id. A leading dash is refused
// because the id is handed to the CLI as the value of --model, where it
// would read as a flag.
func validModel(model string) error {
	switch {
	case model == "":
		return fmt.Errorf("%w: a model is required", ErrInvalidModel)
	case len(model) > maxModelLen:
		return fmt.Errorf("%w: a model is at most %d characters", ErrInvalidModel, maxModelLen)
	case strings.HasPrefix(model, "-"):
		return fmt.Errorf("%w: a model cannot start with a dash", ErrInvalidModel)
	case strings.IndexFunc(model, unicode.IsSpace) >= 0:
		return fmt.Errorf("%w: a model has no whitespace", ErrInvalidModel)
	}
	return nil
}
