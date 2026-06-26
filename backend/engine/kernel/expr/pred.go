package expr

// KnownPred describes one registered predicate's signature for load-time validation (#3).
// Arity 0 = bare predicate (isOwner — written without parentheses, called with arg="").
// Arity 1 = name(tag) form (has, paid).
type KnownPred struct {
	Name  string // identifier as written in formula ("has", "isOwner", "paid")
	Arity int    // 0 = bare; 1 = name(tag)
}

// BasePreds returns the §6 base predicate table.
// has(tag) arity 1; isOwner bare arity 0; paid(tag) arity 1.
// platform/config passes this (optionally extended) as knownPreds to Parse.
func BasePreds() []KnownPred {
	return []KnownPred{
		{Name: "has", Arity: 1},
		{Name: "isOwner", Arity: 0},
		{Name: "paid", Arity: 1},
	}
}
