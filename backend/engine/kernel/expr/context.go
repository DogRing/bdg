package expr

import "github.com/dogring/bdg/engine/kernel/core"

// Context is the abstract operand/predicate channel.
// expr DECLARES this interface; each caller implements it as a thin adapter
// (gates AgentSnapshot, climate CellState, flora SiteInput+Plant, economy portal).
// All three methods are READ-ONLY and must be pure for a given snapshot.
type Context interface {
	// Stat resolves a subject stat ID (Strength, Agility, …) to its numeric value.
	// A StatID referenced by a Program is guaranteed to exist (validated at load),
	// so this returns a plain float64 with no error branch.
	Stat(id core.StatID) float64

	// Attr resolves a target/environment attribute (terrain.depth, moisture, door.lockStrength, …).
	// ok=false means the attribute is absent on this subject; expr maps absence to 0 (fixed policy).
	Attr(name core.Tag) (val float64, ok bool)

	// Pred evaluates a boolean predicate by name and Tag argument.
	// Arity-0 predicates (isOwner) are called with arg="".
	// The name+arity were validated at load; an unknown name can never reach here.
	Pred(name string, arg core.Tag) bool
}

// StatSet is the read-only set of valid StatIDs supplied by platform/config so that
// Parse can reject an undefined uppercase-initial identifier at load time (D10).
type StatSet interface {
	Has(id core.StatID) bool
}
