package world

import (
	"sort"

	"github.com/dogring/bdg/engine/agent"
	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/mind/actions"
	"github.com/dogring/bdg/engine/mind/perception"
	"github.com/dogring/bdg/engine/mind/stats"
)

type combinedIntent struct {
	id         core.ObjectID
	agent      *agent.Intent
	agentIndex int
	animal     *fauna.Intent
}

func (w *World) applyCombinedIntents(
	agentIntents []agent.Intent,
	animalIntents []fauna.Intent,
	applySeed int64,
	newSounds *[]perception.SoundEvent,
) {
	combined := make([]combinedIntent, 0, len(agentIntents)+len(animalIntents))
	agentSeedOffsets := combinedAgentSeedOffsets(agentIntents)
	for i := range agentIntents {
		if agentIntents[i].Kind == agent.IntentNone {
			continue
		}
		combined = append(combined, combinedIntent{
			id:         core.ObjectID(agentIntents[i].Agent),
			agent:      &agentIntents[i],
			agentIndex: agentSeedOffsets[agentIntents[i].Agent],
		})
	}
	for i := range animalIntents {
		combined = append(combined, combinedIntent{id: animalIntents[i].Animal, animal: &animalIntents[i]})
	}
	sort.SliceStable(combined, func(i, j int) bool {
		return string(combined[i].id) < string(combined[j].id)
	})

	conflicts := combinedConflictGroups(combined)
	hideRNG := w.envFork(w.tick, "fauna-hide")
	// Pass 1: agents + each animal's OWN-state commit (fixed sorted order, D12).
	for i := range combined {
		item := combined[i]
		loser := combinedConflictLoser(i, combined, conflicts, w)
		if item.agent != nil {
			w.applyCombinedAgentIntent(*item.agent, loser, applySeed+int64(item.agentIndex), newSounds)
			continue
		}
		if item.animal != nil && !loser {
			w.commitAnimalOwnState(*item.animal)
			w.applyAnimalHiding(w.animals[item.animal.Animal], hideRNG)
		}
	}
	// Pass 2: animal combat cross-writes (attack/feed) + death, AFTER every own-state commit — so an
	// attacker's mutual-engage/damage write to its target is never clobbered by the target's own commit.
	// The mutual lock is thus order-independent (deterministic; the D12 sorted apply order is unchanged).
	// Mating (P_fa4c-2) is a cross-animal write too and rides the same pass, for the same reason: it
	// compares both partners' COMMITTED positions and writes both cooldowns. It resolves after combat
	// on each animal so a partner killed this tick cannot also conceive.
	mateIntents := mateIntentIndex(animalIntents)
	mateFork := w.envFork(w.tick, "fauna-mate")
	for i := range combined {
		item := combined[i]
		if item.animal == nil {
			continue
		}
		if combinedConflictLoser(i, combined, conflicts, w) {
			continue
		}
		w.applyAnimalCombat(*item.animal)
		if mateIntents != nil {
			w.applyAnimalMate(*item.animal, mateIntents, mateFork)
		}
	}
}

// mateIntentIndex builds the partner lookup the mutual-consent check needs (P_fa4c-2). Returns nil
// when no animal is courting this tick, which is the overwhelmingly common case — so a world whose
// content authors no Mate action allocates nothing and behaves byte-identically to pre-P_fa4c-2.
// The map is only ever read by single-key lookup, never ranged (D12).
func mateIntentIndex(animalIntents []fauna.Intent) map[core.ObjectID]fauna.Intent {
	var idx map[core.ObjectID]fauna.Intent
	for i := range animalIntents {
		if animalIntents[i].MateWith == "" {
			continue
		}
		if idx == nil {
			idx = make(map[core.ObjectID]fauna.Intent, len(animalIntents))
		}
		idx[animalIntents[i].Animal] = animalIntents[i]
	}
	return idx
}

func combinedAgentSeedOffsets(agentIntents []agent.Intent) map[core.AgentID]int {
	ids := make([]core.AgentID, 0, len(agentIntents))
	seen := make(map[core.AgentID]bool, len(agentIntents))
	for i := range agentIntents {
		if agentIntents[i].Kind == agent.IntentNone || seen[agentIntents[i].Agent] {
			continue
		}
		seen[agentIntents[i].Agent] = true
		ids = append(ids, agentIntents[i].Agent)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	offsets := make(map[core.AgentID]int, len(ids))
	for i, id := range ids {
		offsets[id] = i
	}
	return offsets
}

func (w *World) applyCombinedAgentIntent(intent agent.Intent, conflictLoser bool, forkSeed int64, sounds *[]perception.SoundEvent) {
	if intent.Kind == agent.IntentSignal {
		w.applySignal(intent)
		return
	}
	fork := rngNew(forkSeed)
	outcomeStatus := agent.Succeeded
	if conflictLoser {
		outcomeStatus = agent.Interrupted
	}
	outcome := w.resolveOutcome(intent, outcomeStatus, fork)
	if outcome.Status == agent.Succeeded {
		w.applyIntent(intent, outcome, sounds)
	}
	a := w.agents[intent.Agent]
	if a != nil {
		a.ApplyOutcome(outcome, w.tick, fork, a.Cfg, w.svc.Stats, w.emit)
	}
}

// applyAnimalIntent applies one animal intent in isolation (own state then combat) — the single-animal
// path used by focused tests. The combined tick apply uses the two-pass commitAnimalOwnState /
// applyAnimalCombat split (applyCombinedIntents) so cross-animal engage/damage writes survive every
// own-state commit regardless of sorted-id order.
func (w *World) applyAnimalIntent(intent fauna.Intent) {
	w.commitAnimalOwnState(intent)
	w.applyAnimalHiding(w.animals[intent.Animal], w.envFork(w.tick, "fauna-hide"))
	w.applyAnimalCombat(intent)
}

// commitAnimalOwnState commits an animal's OWN proposed state from its intent (movement, drives, stamina,
// vital, engage bookkeeping, action effect). Cross-animal effects (attack/feed) + death are deferred to
// applyAnimalCombat so they run after every own-state commit (see applyCombinedIntents pass 1/2).
func (w *World) commitAnimalOwnState(intent fauna.Intent) {
	a := w.animals[intent.Animal]
	if a == nil {
		return
	}
	intended, heading := w.reflectAtBounds(intent.NextPos, intent.NextHeading)
	pos := intended
	if res := w.coverResistance(a.Species, intended); res > 1 {
		pos = a.Pos.Add(intended.Sub(a.Pos).Scale(unitScalar / res))
	}
	w.spatial.Move(a.ID, pos)
	a.Pos = pos
	a.Concealment = zeroScalar
	if cf := w.envCfg.FaunaCombat.ConcealFactor; cf > 0 {
		a.Concealment = w.coverDensity(pos) * cf
	}
	a.Heading = heading
	a.Drives = cloneFaunaDrives(intent.Drives)
	a.Stamina = intent.Stamina
	a.Age += w.envCfg.FaunaDT // PD4-ii/P_fa4c: world advances age by one fauna time-step each tick (§6 `maturity`)
	w.commitAnimalVital(a, intent)
	a.ActiveUntil = intent.ActiveUntil
	a.CurrentAction = intent.Action
	a.EngagedWith = intent.EngagedWith
	a.NextExchangeTick = intent.NextExchangeTick
	a.EngageCooldownUntil = intent.EngageCooldownUntil
	w.layerAnimalActionEffect(a, intent.Action)
	w.applyAnimalFatigue(a, intent.Action)
}

// applyAnimalFatigue (M2 endurance): a high-effort action (chase/flight — effort:high) accrues fatigue; a
// low-effort action (Rest/Graze — effort:none/low) sheds it. The speed §6 reads `- fatigue`, so a prolonged
// chase tires the pursuer (or the fleer) and endurance — not raw speed — decides a long chase. Data-driven
// (CombatParams rates); no-op if the species has no fatigue drive or the rates are zero.
func (w *World) applyAnimalFatigue(a *fauna.Animal, action actions.ActionID) {
	if a == nil || a.Drives == nil {
		return
	}
	if _, ok := a.Drives["fatigue"]; !ok {
		return
	}
	var delta float64
	switch {
	case w.actionHasTag(action, tagEffortHigh):
		delta = w.envCfg.FaunaCombat.FatiguePursuitPerTick
	case w.envCfg.FaunaCombat.SleepFatigueRecoverPerTick > 0 && w.actionHasTag(action, fauna.TagSleep):
		// Torpor (SS2/FM11b): sleeping recovers fatigue DEEPER than ordinary rest. Guarded on a
		// POSITIVE rate (and checked before the effort cases so a state:sleep action, which is also
		// effort:none, gets the deep rate) — an UNSET deep rate falls through to the effort:none case
		// below so a sleeper still recovers at the ordinary rest rate, never LESS than a rester.
		delta = -w.envCfg.FaunaCombat.SleepFatigueRecoverPerTick
	case w.actionHasTag(action, tagEffortNone), w.actionHasTag(action, tagEffortLow):
		delta = -w.envCfg.FaunaCombat.FatigueRecoverPerTick
	}
	if delta != zeroScalar {
		a.Drives["fatigue"] = clamp01(a.Drives["fatigue"] + delta)
	}
}

// applyAnimalCombat applies an animal's CROSS-animal combat effects (attack damage + mutual engage, feed)
// and its own death. Runs in the second apply pass: because every animal's own state is already committed,
// an attacker's write to its target's engage/vital is never clobbered by the target's own commit, so the
// mutual lock is order-independent (deterministic).
func (w *World) applyAnimalCombat(intent fauna.Intent) {
	a := w.animals[intent.Animal]
	if a == nil {
		return
	}
	if w.isAttackIntent(intent) {
		w.applyAnimalAttack(a, intent)
	}
	if w.actionHasTag(intent.Action, fauna.TagFeed) {
		w.applyAnimalFeed(a, intent)
	}
	if w.actionHasTag(intent.Action, fauna.TagSteerFood) {
		w.applyAnimalGraze(a)
	}
	if w.actionHasTag(intent.Action, fauna.TagSteerWater) {
		w.applyAnimalDrink(a)
	}
	if a.Vital <= 0 {
		// PD3 (P_fa4b): reaching the own-state Vital≤0 check means NON-combat vital depletion — a combat
		// kill is already labelled (causePredation) and removed inside applyAnimalAttack, so the only way
		// here is a nextVital bleed. There are now TWO such bleeds (PD3 hunger, PD11 old age), so the
		// label can no longer be a constant: ask fauna for the two magnitudes and attribute the death to
		// whichever channel actually took the Vital. Attribution, not precedence — an old animal that is
		// also starving is recorded honestly, so adding aging never reads as a famine in the telemetry.
		w.killAnimal(a.ID, w.nonCombatDeathCause(*a))
	}
}

// nonCombatDeathCause labels a Vital≤0 death that did NOT come from combat (PD11 / §7 aging). fauna
// reports this tick's two bleed magnitudes — acute drive saturation (PD3 starvation) and old age
// (PD11 senescence) — and the larger one gets the death. Ties keep `starvation`, the pre-existing
// label, so a world with no species authoring a lifespan is byte-identical to before aging existed.
// Deterministic: a pure function of the animal's own state (D12).
func (w *World) nonCombatDeathCause(a fauna.Animal) string {
	if w.faunaRules == nil {
		return causeStarvation
	}
	starve, senesce := w.faunaRules.VitalDrains(a, w.envCfg.FaunaDT)
	if senesce > starve {
		return causeSenescence
	}
	return causeStarvation
}

func (w *World) layerAnimalActionEffect(a *fauna.Animal, action actions.ActionID) {
	def, ok := w.actReg.Get(action)
	if !ok {
		return
	}
	if a.Drives == nil {
		a.Drives = make(map[fauna.DriveID]float64)
	}
	for dim, delta := range def.Effect {
		w.applyAnimalEffect(a, dim, delta)
	}
	for dim, delta := range def.EffectPerMinute {
		w.applyAnimalEffect(a, dim, delta)
	}
}

func (w *World) actionHasTag(action actions.ActionID, tag core.Tag) bool {
	def, ok := w.actReg.Get(action)
	if !ok {
		return false
	}
	for _, t := range def.Tags {
		if t == tag {
			return true
		}
	}
	return false
}

func (w *World) applyAnimalEffect(a *fauna.Animal, dim core.Dimension, delta float64) {
	if dim == "Vital" {
		a.Vital = clamp01(a.Vital + delta)
		return
	}
	id := fauna.DriveID(core.Tag(dim))
	if _, ok := a.Drives[id]; !ok {
		return
	}
	a.Drives[id] = clamp01(a.Drives[id] + delta)
}

type combinedConflictKey string

func combinedConflictGroups(items []combinedIntent) map[combinedConflictKey][]int {
	groups := make(map[combinedConflictKey][]int)
	for i, item := range items {
		target := combinedTarget(item)
		if target == "" {
			continue
		}
		groups[combinedConflictKey(target)] = append(groups[combinedConflictKey(target)], i)
	}
	return groups
}

func combinedConflictLoser(i int, items []combinedIntent, groups map[combinedConflictKey][]int, w *World) bool {
	target := combinedTarget(items[i])
	indices := groups[combinedConflictKey(target)]
	if len(indices) <= 1 {
		return false
	}
	return i != combinedConflictWinner(indices, items, w)
}

func combinedConflictWinner(indices []int, items []combinedIntent, w *World) int {
	winner := indices[0]
	winnerStat := combinedConflictStat(items[winner], w)
	for _, idx := range indices[1:] {
		stat := combinedConflictStat(items[idx], w)
		if stat > winnerStat || (stat == winnerStat && string(items[idx].id) < string(items[winner].id)) {
			winner = idx
			winnerStat = stat
		}
	}
	return winner
}

func combinedTarget(item combinedIntent) core.ObjectID {
	if item.agent != nil {
		if item.agent.Kind == agent.IntentSignal || item.agent.Kind == agent.IntentNone {
			return ""
		}
		return item.agent.Target
	}
	if item.animal != nil {
		return item.animal.Target
	}
	return ""
}

func combinedConflictStat(item combinedIntent, w *World) float64 {
	action := combinedAction(item)
	def, ok := w.actReg.Get(action)
	if !ok {
		return 0
	}
	statIDs := statsFromTags(def.Tags, w.svc.Stats)
	if len(statIDs) == 0 {
		caps := w.svc.Stats.Kinds(stats.Capability)
		if len(caps) > 0 {
			statIDs = []core.StatID{caps[0]}
		}
	}
	if item.agent != nil {
		a := w.agents[item.agent.Agent]
		if a == nil {
			return 0
		}
		return composeStat(a.RealStats, statIDs)
	}
	if item.animal != nil {
		a := w.animals[item.animal.Animal]
		if a == nil {
			return 0
		}
		return composeAnimalStats(a.Stats, statIDs)
	}
	return 0
}

func combinedAction(item combinedIntent) actions.ActionID {
	if item.agent != nil {
		return item.agent.Action
	}
	if item.animal != nil {
		return item.animal.Action
	}
	return ""
}

func composeAnimalStats(values map[core.StatID]float64, ids []core.StatID) float64 {
	if len(ids) == 0 {
		return 0
	}
	var sum float64
	for _, id := range ids {
		sum += values[id]
	}
	return sum / float64(len(ids))
}
