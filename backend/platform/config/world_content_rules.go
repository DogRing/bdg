package config

import (
	"fmt"
	"strconv"

	"github.com/dogring/bdg/engine/env/climate"
	"github.com/dogring/bdg/engine/env/decay"
	"github.com/dogring/bdg/engine/env/flora"
	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/expr"
	"github.com/dogring/bdg/engine/mind/actions"
	"github.com/dogring/bdg/engine/mind/stats"
	"github.com/dogring/bdg/engine/space/navmap"
	"gopkg.in/yaml.v3"
)

func buildClimateRules(cd climateDoc, terrain map[navmap.TerrainID]navmap.TerrainType, statReg *stats.Registry) (*climate.Rules, error) {
	allowed := tagSet("moisture", "temperature")
	rules := make([]climate.TransitionRule, 0, len(cd.Transitions))
	iceType := core.Tag(cd.Balance.IceType)
	if iceType != "" {
		if _, ok := terrain[navmap.TerrainID(iceType)]; !ok {
			return nil, fmt.Errorf("config: climate ice_type %q is not a terrain id", iceType)
		}
	}
	hasOriginRule := false
	hasFreezeRule := false
	for i, tr := range cd.Transitions {
		from, to := core.Tag(tr.From), core.Tag(tr.To)
		if _, ok := terrain[navmap.TerrainID(from)]; !ok {
			return nil, fmt.Errorf("config: climate transition %d unknown from terrain %q", i, from)
		}
		if to != climate.OriginTerrain {
			if _, ok := terrain[navmap.TerrainID(to)]; !ok {
				return nil, fmt.Errorf("config: climate transition %d unknown to terrain %q", i, to)
			}
		} else {
			hasOriginRule = true
		}
		if iceType != "" && to == iceType {
			hasFreezeRule = true
		}
		prog, err := expr.Parse(tr.When, expr.KindBool, statSet{statReg}, expr.BasePreds())
		if err != nil {
			return nil, fmt.Errorf("config: climate transition %d when: %w", i, err)
		}
		if err := checkProgramAttrs("climate transition "+strconv.Itoa(i), prog, allowed); err != nil {
			return nil, err
		}
		if len(prog.ReadsPreds()) > 0 {
			return nil, fmt.Errorf("config: climate transition %d predicates are not allowed", i)
		}
		rules = append(rules, climate.TransitionRule{From: from, When: prog, To: to})
	}
	if hasOriginRule && iceType == "" {
		return nil, fmt.Errorf("config: climate transition to %q requires balance.ice_type", climate.OriginTerrain)
	}
	if hasOriginRule && !hasFreezeRule {
		return nil, fmt.Errorf("config: climate transition to %q requires a freeze rule targeting ice_type %q", climate.OriginTerrain, iceType)
	}
	return climate.NewRules(rules), nil
}

func buildFloraRules(doc objectsDoc, itemIDs map[core.Tag]bool, terrainAttrs map[core.Tag]bool, statReg *stats.Registry) (*flora.Rules, error) {
	allowed := cloneTagSet(terrainAttrs)
	for _, k := range []core.Tag{"moisture", "temperature", "thermal_stress", "width", "length", "neighbor_count"} {
		allowed[k] = true
	}
	species := make(map[flora.SpeciesID]flora.SpeciesRule)
	for _, obj := range sortedObjectKinds(doc.ObjectKinds) {
		if obj.Flora == nil {
			continue
		}
		parseNum := func(name string, v any) (*expr.Program, error) {
			return parseNumericFormula("flora "+obj.ID+" "+name, v, statReg, allowed)
		}
		suit, err := parseNum("suitability", obj.Flora.Suitability)
		if err != nil {
			return nil, err
		}
		lenRate, err := parseNum("length_rate", obj.Flora.LengthRate)
		if err != nil {
			return nil, err
		}
		widRate, err := parseNum("width_rate", obj.Flora.WidthRate)
		if err != nil {
			return nil, err
		}
		shadeRadius, err := parseNum("shade.radius", obj.Flora.Shade.Radius)
		if err != nil {
			return nil, err
		}
		shadeOpacity, err := parseNum("shade.opacity", obj.Flora.Shade.Opacity)
		if err != nil {
			return nil, err
		}
		propRadius, err := parseNum("propagation.radius", obj.Flora.Propagation.Radius)
		if err != nil {
			return nil, err
		}
		if containsTag(propRadius.ReadsAttrs(), "neighbor_count") {
			return nil, fmt.Errorf("config: flora %s propagation.radius must not read neighbor_count", obj.ID)
		}
		propChance, err := parseNum("propagation.chance", obj.Flora.Propagation.Chance)
		if err != nil {
			return nil, err
		}
		// carrying_capacity (1k): scalar OR §6(terrain attrs, climate) → K. Absent → nil program
		// (legacy 1/(1+n)). A literal negative scalar is a content error; a formula may dip ≤0 at a
		// site (clamped to density 0 at runtime — e.g. water via (1−depth)).
		var carryingCap *expr.Program
		if raw := obj.Flora.Propagation.CarryingCapacity; raw != nil {
			if f, ok := asNumber(raw); ok && f < 0 {
				return nil, fmt.Errorf("config: flora %s propagation.carrying_capacity must be >= 0 (a formula may evaluate ≤0 per-site; a literal negative constant is disallowed)", obj.ID)
			}
			carryingCap, err = parseNum("propagation.carrying_capacity", raw)
			if err != nil {
				return nil, err
			}
			if containsTag(carryingCap.ReadsAttrs(), "neighbor_count") {
				return nil, fmt.Errorf("config: flora %s propagation.carrying_capacity must not read neighbor_count (circular)", obj.ID)
			}
			// K is temperature-free by contract (1m): it is evaluated BOTH at runtime and by
			// world-gen density placement, and placement runs before climate exists
			// (docs/plans/scaling.md SC4) so it has no temperature to supply. A temperature term
			// would silently mean two different things in the two contexts. Temperature belongs
			// in suitability — name it, so the author is redirected rather than just blocked.
			for _, attr := range []core.Tag{"temperature", "thermal_stress"} {
				if containsTag(carryingCap.ReadsAttrs(), attr) {
					return nil, fmt.Errorf("config: flora %s propagation.carrying_capacity must not read %s — density placement evaluates K before climate exists, so a temperature term would mean something different there; put the temperature response in suitability instead", obj.ID, attr)
				}
			}
		}
		if err := checkAscending("flora "+obj.ID+" stages", obj.Flora.Stages, false); err != nil {
			return nil, err
		}
		rule := flora.SpeciesRule{
			Suitability:      suit,
			LengthRate:       lenRate,
			WidthRate:        widRate,
			ShadeRadius:      shadeRadius,
			ShadeOpacity:     shadeOpacity,
			Stages:           append([]float64(nil), obj.Flora.Stages...),
			YieldStage:       obj.Flora.YieldStage,
			PropagateStage:   obj.Flora.PropagateStage,
			PropRadius:       propRadius,
			PropChance:       propChance,
			CarryingCapacity: carryingCap,
			ComfortTemp:      obj.Flora.ComfortTemp,
			ThermalBand:      obj.Flora.ThermalBand,
			DeathThreshold:   obj.Flora.DeathThreshold,
			DeathHysteresis:  obj.Flora.DeathHysteresis,
		}
		yields, err := floraYields(obj, itemIDs, statReg, allowed)
		if err != nil {
			return nil, err
		}
		rule.Yields = yields
		if err := checkFloraThermalBand(obj.ID, rule); err != nil {
			return nil, err
		}
		species[flora.SpeciesID(obj.ID)] = rule
	}
	if len(species) == 0 {
		return nil, nil
	}
	return flora.NewRules(species), nil
}

func floraYields(obj objectKindDoc, itemIDs map[core.Tag]bool, statReg *stats.Registry, allowed map[core.Tag]bool) ([]flora.YieldRule, error) {
	if obj.Harvest == nil {
		return nil, nil
	}
	var rows []struct {
		Item   string `yaml:"item"`
		Chance any    `yaml:"chance"`
		Qty    []int  `yaml:"qty"`
	}
	data, err := yaml.Marshal(obj.Harvest.Yields)
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(data, &rows); err != nil || len(rows) == 0 {
		return nil, nil
	}
	out := make([]flora.YieldRule, 0, len(rows))
	for i, row := range rows {
		item := core.Tag(row.Item)
		if !itemIDs[item] {
			return nil, fmt.Errorf("config: flora %s yield %d unknown item %q", obj.ID, i, item)
		}
		chance, err := parseNumericFormula("flora "+obj.ID+" yield chance", row.Chance, statReg, allowed)
		if err != nil {
			return nil, err
		}
		if len(row.Qty) != 2 {
			return nil, fmt.Errorf("config: flora %s yield %d qty must have two values", obj.ID, i)
		}
		out = append(out, flora.YieldRule{Item: item, Chance: chance, QtyMin: row.Qty[0], QtyMax: row.Qty[1]})
	}
	return out, nil
}

func buildFaunaRules(doc objectsDoc, terrainIDs map[core.Tag]bool, statReg *stats.Registry, actReg *actions.Registry) (*fauna.Rules, error) {
	baseAllowed := tagSet(fauna.AttrOperands()...)
	species := make(map[fauna.SpeciesID]fauna.SpeciesRule)
	for _, obj := range sortedObjectKinds(doc.ObjectKinds) {
		if obj.Fauna == nil {
			continue
		}
		drives, allowed := buildFaunaDrives(obj, baseAllowed)
		parse := func(name string, v any) (*expr.Program, error) {
			return parseNumericFormula("fauna "+obj.ID+" "+name, v, statReg, allowed)
		}
		utilities, steer, err := buildFaunaUtilities(obj, actReg, parse)
		if err != nil {
			return nil, err
		}
		appTemp, err := parse("apparent_temp", obj.Fauna.ApparentTemp)
		if err != nil {
			return nil, err
		}
		speed, err := parse("speed", obj.Fauna.Speed)
		if err != nil {
			return nil, err
		}
		turnRate, err := parseOptionalFaunaFormula(obj.Fauna.TurnRate, parse, "turn_rate")
		if err != nil {
			return nil, err
		}
		scentAcuity, err := parseOptionalFaunaFormula(obj.Fauna.ScentAcuity, parse, "scent_acuity")
		if err != nil {
			return nil, err
		}
		attackPower, err := parseOptionalFaunaFormula(obj.Fauna.AttackPower, parse, "attack_power")
		if err != nil {
			return nil, err
		}
		hit, err := parseOptionalFaunaFormula(obj.Fauna.Hit, parse, "hit")
		if err != nil {
			return nil, err
		}
		feed, err := parseOptionalFaunaFormula(obj.Fauna.Feed, parse, "feed")
		if err != nil {
			return nil, err
		}
		graze, err := parseOptionalFaunaFormula(obj.Fauna.Graze, parse, "graze")
		if err != nil {
			return nil, err
		}
		drink, err := parseOptionalFaunaFormula(obj.Fauna.Drink, parse, "drink")
		if err != nil {
			return nil, err
		}
		hideChance, err := parseOptionalFaunaFormula(obj.Fauna.HideChance, parse, "hide_chance")
		if err != nil {
			return nil, err
		}
		tc, imp, err := faunaTerrain(obj, terrainIDs)
		if err != nil {
			return nil, err
		}
		species[fauna.SpeciesID(obj.ID)] = fauna.SpeciesRule{
			Utilities: utilities, Drives: drives, AppTemp: appTemp,
			ComfortTemp: obj.Fauna.ComfortTemp, ThermalBand: obj.Fauna.ThermalBand,
			HazardAvoidance: obj.Fauna.HazardAvoidance, MoveDeadband: obj.Fauna.MoveDeadband, MaturityAge: obj.Fauna.MaturityAge, MateCooldown: core.Tick(obj.Fauna.MateCooldown), Speed: speed,
			TurnRate: turnRate, ScentAcuity: scentAcuity, AttackPower: attackPower, Hit: hit, Feed: feed, Graze: graze, Drink: drink, HideChance: hideChance,
			CoverCost: obj.Fauna.CoverCost,
			Diet:      faunaDiet(obj), Tags: faunaTags(obj), IsPredator: faunaIsPredator(obj), SmellRadius: obj.Fauna.Senses.SmellRadius,
			SightRadius: obj.Fauna.Senses.SightRadius, FovArc: obj.Fauna.Senses.FovArc,
			TerrainCost: tc, Impassable: imp, SteerChannel: steer,
		}
	}
	if len(species) == 0 {
		return nil, nil
	}
	return fauna.NewRules(species), nil
}

func parseOptionalFaunaFormula(v any, parse func(string, any) (*expr.Program, error), name string) (*expr.Program, error) {
	if v == nil {
		return nil, nil
	}
	return parse(name, v)
}

func buildDecayRules(doc objectsDoc, itemIDs map[core.Tag]bool, statReg *stats.Registry) (*decay.Rules, error) {
	allowed := tagSet("temperature", "moisture")
	kinds := make(map[decay.KindID]decay.KindRule)
	for _, item := range sortedItemKinds(doc.ItemKinds) {
		if item.Decay == nil {
			continue
		}
		accel, err := parseNumericFormula("decay "+item.ID+" accel", item.Decay.Accel, statReg, allowed)
		if err != nil {
			return nil, err
		}
		states, err := decayStates(item, itemIDs)
		if err != nil {
			return nil, err
		}
		kinds[decay.KindID(item.ID)] = decay.KindRule{BaseRate: item.Decay.BaseRate, Accel: accel, States: states}
	}
	if len(kinds) == 0 {
		return nil, nil
	}
	return decay.NewRules(kinds), nil
}
