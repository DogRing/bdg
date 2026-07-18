package config

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/dogring/bdg/engine/env/decay"
	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/expr"
	"github.com/dogring/bdg/engine/mind/actions"
	"github.com/dogring/bdg/engine/mind/stats"
	"github.com/dogring/bdg/engine/space/navmap"
)

func parseNumericFormula(label string, v any, statReg *stats.Registry, allowedAttrs map[core.Tag]bool) (*expr.Program, error) {
	text, err := formulaText(v)
	if err != nil {
		return nil, fmt.Errorf("config: %s formula: %w", label, err)
	}
	prog, err := expr.Parse(text, expr.KindNum, statSet{statReg}, expr.BasePreds())
	if err != nil {
		return nil, fmt.Errorf("config: %s formula: %w", label, err)
	}
	if err := checkProgramAttrs(label, prog, allowedAttrs); err != nil {
		return nil, err
	}
	if preds := prog.ReadsPreds(); len(preds) > 0 {
		return nil, fmt.Errorf("config: %s formula predicates are not allowed", label)
	}
	return prog, nil
}

// asNumber reports whether v is a plain numeric scalar (not a formula string) and its value.
// Used to reject a literal-negative constant while allowing §6 formula strings (which may
// legitimately dip ≤0 per-site and are clamped at runtime).
func asNumber(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case float64:
		return x, true
	case float32:
		return float64(x), true
	default:
		return 0, false
	}
}

func formulaText(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case int:
		return strconv.Itoa(x), nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case float64:
		return strconv.FormatFloat(x, 'g', 17, 64), nil
	case float32:
		return strconv.FormatFloat(float64(x), 'g', 9, 32), nil
	default:
		return "", fmt.Errorf("unsupported formula value %T", v)
	}
}

func checkProgramAttrs(label string, prog *expr.Program, allowed map[core.Tag]bool) error {
	for _, attr := range prog.ReadsAttrs() {
		if !allowed[attr] {
			return fmt.Errorf("config: %s unknown operand %q", label, attr)
		}
	}
	return nil
}

func containsTag(tags []core.Tag, needle core.Tag) bool {
	for _, tag := range tags {
		if tag == needle {
			return true
		}
	}
	return false
}

func checkAscending(label string, xs []float64, allowEqualFirstZero bool) error {
	for i := 1; i < len(xs); i++ {
		if xs[i] <= xs[i-1] {
			if allowEqualFirstZero && i == 1 && xs[0] == 0 && xs[1] > xs[0] {
				continue
			}
			return fmt.Errorf("config: %s must be strictly ascending", label)
		}
	}
	return nil
}

func checkObjectTerrainRefs(doc objectsDoc, terrain map[core.Tag]bool) error {
	if len(terrain) == 0 {
		return nil
	}
	for _, obj := range sortedObjectKinds(doc.ObjectKinds) {
		if obj.Source != nil && obj.Source.DepletedTerrain != "" {
			id := core.Tag(obj.Source.DepletedTerrain)
			if !terrain[id] {
				return fmt.Errorf("config: object %s source depleted_terrain unknown terrain %q", obj.ID, id)
			}
		}
	}
	return nil
}

func sortedObjectKinds(in []objectKindDoc) []objectKindDoc {
	out := append([]objectKindDoc(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func buildScentEmitters(doc objectsDoc) map[core.Tag][]core.Tag {
	emitters := make(map[core.Tag][]core.Tag)
	for _, obj := range sortedObjectKinds(doc.ObjectKinds) {
		addScentEmitterTags(emitters, core.Tag(obj.ID), obj.Tags)
	}
	for _, item := range sortedItemKinds(doc.ItemKinds) {
		addScentEmitterTags(emitters, core.Tag(item.ID), item.Tags)
	}
	if len(emitters) == 0 {
		return nil
	}
	return emitters
}

func buildCoverKinds(doc objectsDoc) map[core.Tag]bool {
	out := make(map[core.Tag]bool)
	for _, obj := range sortedObjectKinds(doc.ObjectKinds) {
		for _, tag := range obj.Tags {
			if tag == "cover" {
				out[core.Tag(obj.ID)] = true
				break
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// buildWindBlockerKinds collects object kinds tagged `blocks_wind` (docs/plans/shelter.md SH1). worldgen
// turns each placed instance of such a kind into an exposure.Blocker (wind-shadow caster). No
// tagged kinds ⇒ nil ⇒ shelter stays OFF (ε ≡ 1, byte-identical). Mirrors buildCoverKinds.
func buildWindBlockerKinds(doc objectsDoc) map[core.Tag]bool {
	return buildTaggedKinds(doc, "blocks_wind")
}

// buildCovererKinds collects object kinds tagged `covers` (docs/plans/shelter.md SH3). worldgen turns each
// placed instance into an exposure.Coverer (overhead-cover caster). No tagged kinds ⇒ nil ⇒ SH3 OFF
// (ε_cover ≡ 1, byte-identical). Mirrors buildWindBlockerKinds.
func buildCovererKinds(doc objectsDoc) map[core.Tag]bool {
	return buildTaggedKinds(doc, "covers")
}

// buildTaggedKinds returns the set of object-kind IDs carrying the given tag (nil if none).
func buildTaggedKinds(doc objectsDoc, tag core.Tag) map[core.Tag]bool {
	out := make(map[core.Tag]bool)
	for _, obj := range sortedObjectKinds(doc.ObjectKinds) {
		for _, t := range obj.Tags {
			if core.Tag(t) == tag {
				out[core.Tag(obj.ID)] = true
				break
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func addScentEmitterTags(emitters map[core.Tag][]core.Tag, kind core.Tag, tags []string) {
	seen := make(map[core.Tag]bool)
	for _, tagText := range tags {
		if !strings.HasPrefix(tagText, "scent:") {
			continue
		}
		tag := core.Tag(tagText)
		if seen[tag] {
			continue
		}
		seen[tag] = true
		emitters[kind] = append(emitters[kind], tag)
	}
	sort.Slice(emitters[kind], func(i, j int) bool { return emitters[kind][i] < emitters[kind][j] })
}

func sortedItemKinds(in []itemKindDoc) []itemKindDoc {
	out := append([]itemKindDoc(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func tagSet(tags ...core.Tag) map[core.Tag]bool {
	out := make(map[core.Tag]bool, len(tags))
	for _, t := range tags {
		out[t] = true
	}
	return out
}

func cloneTagSet(in map[core.Tag]bool) map[core.Tag]bool {
	out := make(map[core.Tag]bool, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func terrainIDSet(types map[navmap.TerrainID]navmap.TerrainType) map[core.Tag]bool {
	out := make(map[core.Tag]bool, len(types))
	for id := range types {
		out[core.Tag(id)] = true
	}
	return out
}

func terrainAttrSet(td terrainDoc) map[core.Tag]bool {
	out := make(map[core.Tag]bool)
	for _, t := range td.Terrains {
		for attr := range t.Attrs {
			out[core.Tag(attr)] = true
		}
	}
	return out
}

// deriveDrinkableTerrains (FM4-src, docs/plans/fauna.md §4.1) computes the set of terrain IDs whose water is
// DRINKABLE from terrain.yaml attrs: fresh (salinity ≤ salinityMax) AND open water (moisture ≥ moistureMin).
// This is the config-time, data-defined source for the fauna water-attraction field — no Go terrain-name
// hardcoding, and no runtime terrainAttrs wiring (attrs are per-TYPE uniform, so load-time derivation
// suffices). moistureMin ≤ 0 ⇒ OFF (nil ⇒ no water field, byte-identical). Membership set (order-free, D12).
func deriveDrinkableTerrains(td terrainDoc, salinityMax, moistureMin float64) map[core.Tag]bool {
	if moistureMin <= 0 {
		return nil
	}
	out := make(map[core.Tag]bool)
	for _, t := range td.Terrains {
		if t.Attrs["moisture"] >= moistureMin && t.Attrs["salinity"] <= salinityMax {
			out[core.Tag(t.ID)] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func buildFaunaDrives(obj objectKindDoc, baseAllowed map[core.Tag]bool) ([]fauna.DriveRule, map[core.Tag]bool) {
	allowed := cloneTagSet(baseAllowed)
	drives := make([]fauna.DriveRule, 0, len(obj.Fauna.Drives))
	for _, d := range obj.Fauna.Drives {
		id := core.Tag(d.ID)
		allowed[id] = true
		drives = append(drives, fauna.DriveRule{ID: fauna.DriveID(id), Rate: d.Rate, Decay: d.Decay, WaryLevel: d.WaryLevel, FleeLevel: d.FleeLevel, VitalDrain: d.VitalDrain, VitalDrainAbove: d.VitalDrainAbove})
	}
	return drives, allowed
}

func buildFaunaUtilities(
	obj objectKindDoc,
	actReg *actions.Registry,
	parse func(string, any) (*expr.Program, error),
) (map[actions.ActionID]*expr.Program, map[actions.ActionID]core.Tag, error) {
	utilities := make(map[actions.ActionID]*expr.Program, len(obj.Fauna.Actions))
	steer := make(map[actions.ActionID]core.Tag)
	for _, a := range obj.Fauna.Actions {
		id := actions.ActionID(a.Action)
		if !actReg.Has(id) {
			return nil, nil, fmt.Errorf("config: fauna %s unknown action %q", obj.ID, id)
		}
		prog, err := parse("utility "+a.Action, a.Utility)
		if err != nil {
			return nil, nil, err
		}
		utilities[id] = prog
		if def, ok := actReg.Get(id); ok {
			for _, tag := range def.Tags {
				switch tag {
				case fauna.TagSteerFood, fauna.TagSteerWater, fauna.TagSteerPrey, fauna.TagFleePred, fauna.TagWaryPred, fauna.TagNoLoco, fauna.TagSleep, fauna.TagAttack, fauna.TagFeed:
					steer[id] = tag
				}
			}
		}
	}
	return utilities, steer, nil
}

func faunaTerrain(obj objectKindDoc, terrainIDs map[core.Tag]bool) (map[core.Tag]float64, []core.Tag, error) {
	tc := make(map[core.Tag]float64, len(obj.Fauna.TerrainCost))
	for k, v := range obj.Fauna.TerrainCost {
		t := core.Tag(k)
		if len(terrainIDs) > 0 && !terrainIDs[t] {
			return nil, nil, fmt.Errorf("config: fauna %s terrain_cost unknown terrain %q", obj.ID, t)
		}
		tc[t] = v
	}
	imp := make([]core.Tag, len(obj.Fauna.Impassable))
	for i, k := range obj.Fauna.Impassable {
		t := core.Tag(k)
		if len(terrainIDs) > 0 && !terrainIDs[t] {
			return nil, nil, fmt.Errorf("config: fauna %s impassable unknown terrain %q", obj.ID, t)
		}
		imp[i] = t
	}
	return tc, imp, nil
}

func faunaDiet(obj objectKindDoc) []core.Tag {
	diet := make([]core.Tag, len(obj.Fauna.Diet))
	for i, d := range obj.Fauna.Diet {
		diet[i] = core.Tag(d)
	}
	return diet
}

// faunaTags copies the kind's own content tags so fauna.Rules can match another animal's Diet against them
// (D10 tag-driven diet, e.g. wolf diet [game] matches a deer carrying the `game` tag).
func faunaTags(obj objectKindDoc) []core.Tag {
	tags := make([]core.Tag, len(obj.Tags))
	for i, t := range obj.Tags {
		tags[i] = core.Tag(t)
	}
	return tags
}

func faunaIsPredator(obj objectKindDoc) bool {
	for _, tag := range obj.Tags {
		if tag == "threat:predator" {
			return true
		}
	}
	return false
}

// buildRespawnTargets collects the per-species population carrying capacity for timer-respawn (F9). A
// species with respawn_target > 0 is topped up toward that count; absent/0 ⇒ no respawn.
func buildRespawnTargets(doc objectsDoc) map[core.Tag]int {
	out := make(map[core.Tag]int)
	for _, obj := range sortedObjectKinds(doc.ObjectKinds) {
		if obj.Fauna != nil && obj.Fauna.RespawnTarget > 0 {
			out[core.Tag(obj.ID)] = obj.Fauna.RespawnTarget
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func decayStates(item itemKindDoc, itemIDs map[core.Tag]bool) ([]decay.StateRule, error) {
	states := make([]decay.StateRule, 0, len(item.Decay.States))
	thresholds := make([]float64, 0, len(item.Decay.States))
	for _, st := range item.Decay.States {
		thresholds = append(thresholds, st.Threshold)
		supply := make(map[core.Dimension]float64, len(st.Supply))
		for k, v := range st.Supply {
			supply[core.Dimension(k)] = v
		}
		transforms := make([]decay.TransformRule, 0, len(st.Transform))
		for _, tr := range st.Transform {
			target := core.Tag(tr.Item)
			if !itemIDs[target] {
				return nil, fmt.Errorf("config: decay %s transform unknown item %q", item.ID, target)
			}
			transforms = append(transforms, decay.TransformRule{Item: decay.KindID(target), Qty: tr.Qty})
		}
		states = append(states, decay.StateRule{Threshold: st.Threshold, Supply: supply, Transform: transforms})
	}
	if err := checkAscending("decay "+item.ID+" thresholds", thresholds, true); err != nil {
		return nil, err
	}
	return states, nil
}
