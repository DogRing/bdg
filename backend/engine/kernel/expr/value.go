package expr

// Kind tags a Value as numeric or boolean.
// There is NO implicit coercion between them (RESOLVED #7).
type Kind uint8

const (
	KindNum  Kind = iota // float64 payload; arithmetic / cost / suitability context
	KindBool             // bool   payload; gate / condition context
)

// Value is the runtime result of evaluating a Program or a single AST node.
// Callers normally use EvalNumber / EvalBool rather than reading this directly.
type Value struct {
	Kind Kind
	Num  float64 // valid iff Kind == KindNum
	Bool bool    // valid iff Kind == KindBool
}
