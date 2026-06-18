# content/stats.yaml — stat definitions (data-driven, D10)
# The config module loads this at startup to populate stats.Registry. To add a stat, add an entry here (no code).
# Schema: content/schema/stats.schema.json

schema_version: 1

# kind: capability (read by capability gates / outcomes / prediction) | disposition (goal weighting)
stats:
  - id: Strength
    kind: capability
    range: [0.0, 1.0]
    gen: { dist: normal, mean: 0.5, sd: 0.15 }   # agent-generation distribution
    inherit: 0.5                                   # parent inheritance weight (the rest is perturbation)

  - id: Agility
    kind: capability
    range: [0.0, 1.0]
    gen: { dist: normal, mean: 0.5, sd: 0.15 }
    inherit: 0.5

  - id: Intelligence
    kind: capability       # gates abstraction-ladder height, ToM depth, and lookahead
    range: [0.0, 1.0]
    gen: { dist: normal, mean: 0.5, sd: 0.18 }
    inherit: 0.5

  - id: Aggression
    kind: disposition
    range: [0.0, 1.0]
    gen: { dist: normal, mean: 0.4, sd: 0.2 }
    inherit: 0.4
  - id: Impulsivity
    kind: disposition
    range: [0.0, 1.0]
    gen: { dist: normal, mean: 0.5, sd: 0.2 }
    inherit: 0.4
  - id: Honesty
    kind: disposition
    range: [0.0, 1.0]
    gen: { dist: normal, mean: 0.6, sd: 0.2 }
    inherit: 0.4
  - id: Greed
    kind: disposition
    range: [0.0, 1.0]
    gen: { dist: normal, mean: 0.5, sd: 0.2 }
    inherit: 0.4
  - id: Sociability
    kind: disposition
    range: [0.0, 1.0]
    gen: { dist: normal, mean: 0.5, sd: 0.2 }
    inherit: 0.4
  - id: Vindictiveness
    kind: disposition
    range: [0.0, 1.0]
    gen: { dist: normal, mean: 0.4, sd: 0.2 }
    inherit: 0.4
  - id: RiskAversion
    kind: disposition
    range: [0.0, 1.0]
    gen: { dist: normal, mean: 0.5, sd: 0.2 }
    inherit: 0.4

# Global constants (self-calibration rate beta, gossip rate alpha, mood lambda, ...) live in content/balance.yaml.